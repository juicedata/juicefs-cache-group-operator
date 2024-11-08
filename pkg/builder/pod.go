// Copyright 2024 Juicedata Inc
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package builder

import (
	"context"
	"strings"

	juicefsiov1 "github.com/juicedata/juicefs-cache-group-operator/api/v1"
	"github.com/juicedata/juicefs-cache-group-operator/pkg/common"
	"github.com/juicedata/juicefs-cache-group-operator/pkg/utils"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

var (
	secretKeys = []string{
		"token",
		"access-key",
		"access-key2",
		"bucket",
		"bucket2",
		"subdir",
		"secret-key",
		"secret-key2",
	}

	secretStrippedEnvs = []string{
		"token", "secret-key", "secret-key2",
	}
	secretStrippedEnvMap = map[string]string{
		"token":       "TOKEN",
		"secret-key":  "SECRET_KEY",
		"secret-key2": "SECRET_KEY_2",
	}
	secretStrippedEnvOptional = map[string]bool{
		"secret-key2": true,
	}
)

type PodBuilder struct {
	volName    string
	cg         *juicefsiov1.CacheGroup
	node       string
	spec       juicefsiov1.CacheGroupWorkerTemplate
	secretData map[string]string
	initConfig string
}

func NewPodBuilder(cg *juicefsiov1.CacheGroup, secret *corev1.Secret, node string, spec juicefsiov1.CacheGroupWorkerTemplate) *PodBuilder {
	secretData := utils.ParseSecret(secret)
	initconfig := ""
	if v, ok := secretData["initconfig"]; ok && v != "" {
		initconfig = v
	}
	return &PodBuilder{
		secretData: secretData,
		volName:    secretData["name"],
		cg:         cg,
		node:       node,
		spec:       spec,
		initConfig: initconfig,
	}
}

func GenCacheGroupName(cg *juicefsiov1.CacheGroup) string {
	if cg.Spec.CacheGroup != "" {
		return cg.Spec.CacheGroup
	}
	return cg.Namespace + "-" + cg.Name
}

func newBasicPod(cg *juicefsiov1.CacheGroup, nodeName string) *corev1.Pod {
	workerName := common.GenWorkerName(cg.Name, nodeName)
	worker := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        workerName,
			Namespace:   cg.Namespace,
			Annotations: map[string]string{},
			Labels: map[string]string{
				common.LabelCacheGroup: cg.Name,
				common.LabelWorker:     common.LabelWorkerValue,
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: cg.APIVersion,
					Kind:       cg.Kind,
					Name:       cg.Name,
					UID:        cg.UID,
					Controller: utils.ToPtr(true),
				},
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				SecurityContext: &corev1.SecurityContext{
					Privileged: utils.ToPtr(true),
				},
			}},
			NodeName: nodeName,
		},
	}
	return worker
}

func (p *PodBuilder) genEnvs() []corev1.EnvVar {
	envs := []corev1.EnvVar{}
	for _, k := range secretStrippedEnvs {
		_, isOptional := secretStrippedEnvOptional[k]
		envs = append(envs, corev1.EnvVar{
			Name: secretStrippedEnvMap[k],
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					Key:      k,
					Optional: &isOptional,
					LocalObjectReference: corev1.LocalObjectReference{
						Name: p.cg.Spec.SecretRef.Name,
					},
				},
			},
		})
	}
	envs = append(envs, p.spec.Env...)
	return envs
}

func (p *PodBuilder) genAuthCmds(ctx context.Context) []string {
	if p.initConfig != "" {
		conf := p.volName + ".conf"
		authCmds := []string{
			"cp",
			"/etc/juicefs/" + conf,
			"/root/.juicefs",
		}
		return authCmds
	}
	authCmds := []string{
		common.JuiceFSBinary,
		"auth",
		p.volName,
	}

	for _, key := range secretKeys {
		if value, ok := p.secretData[key]; ok {
			if strippedKey, ok := secretStrippedEnvMap[key]; ok {
				authCmds = append(authCmds, "--"+key, "${"+strippedKey+"}")
			} else {
				authCmds = append(authCmds, "--"+key, value)
			}
		}
	}

	// add more options with key `format-options`
	if value, ok := p.secretData["format-options"]; ok {
		formatOptions := utils.ParseOptions(ctx, strings.Split(value, ","))
		for _, opt := range formatOptions {
			if opt[1] != "" {
				authCmds = append(authCmds, "--"+opt[0], opt[1])
			} else {
				authCmds = append(authCmds, "--"+opt[0])
			}
		}
	}

	return authCmds
}

func (p *PodBuilder) genCommands(ctx context.Context) []string {
	authCmds := p.genAuthCmds(ctx)
	cacheGroup := GenCacheGroupName(p.cg)
	mountCmds := []string{
		"exec",
		common.JuiceFsMountBinary,
		p.volName,
		common.MountPoint,
	}

	opts := []string{
		"foreground",
		"cache-group=" + cacheGroup,
	}

	cacheDirs := []string{}
	parsedOpts := utils.ParseOptions(ctx, p.spec.Opts)
	for _, opt := range parsedOpts {
		if opt[0] == "cache-dir" {
			if opt[1] == "" {
				log.FromContext(ctx).Info("invalid cache-dir option", "option", opt)
				continue
			}
			cacheDirs = strings.Split(opt[1], ":")
			continue
		}
		if opt[1] != "" {
			opts = append(opts, strings.TrimSpace(opt[0])+"="+strings.TrimSpace(opt[1]))
		} else {
			opts = append(opts, strings.TrimSpace(opt[0]))
		}
	}

	if len(cacheDirs) == 0 {
		cacheDirs = append(cacheDirs, "/var/jfsCache")
	}
	opts = append(opts, "cache-dir="+strings.Join(cacheDirs, ":"))
	mountCmds = append(mountCmds, "-o", strings.Join(opts, ","))
	cmds := []string{
		"sh",
		"-c",
		strings.Join(authCmds, " ") + "\n" + strings.Join(mountCmds, " "),
	}
	return cmds
}

func (p *PodBuilder) genInitConfigVolumes() {
	if p.initConfig == "" {
		return
	}
	p.spec.Volumes = append(p.spec.Volumes, corev1.Volume{
		Name: "init-config",
		VolumeSource: corev1.VolumeSource{
			Secret: &corev1.SecretVolumeSource{
				SecretName: p.cg.Spec.SecretRef.Name,
				Items: []corev1.KeyToPath{
					{
						Key:  "initconfig",
						Path: p.volName + ".conf",
					},
				},
			},
		},
	})

	p.spec.VolumeMounts = append(p.spec.VolumeMounts, corev1.VolumeMount{
		Name:      "init-config",
		MountPath: "/etc/juicefs",
	})
}

func (p *PodBuilder) NewCacheGroupWorker(ctx context.Context) *corev1.Pod {
	worker := newBasicPod(p.cg, p.node)
	p.genInitConfigVolumes()
	spec := p.spec
	if spec.HostNetwork != nil {
		worker.Spec.HostNetwork = *spec.HostNetwork
	} else {
		worker.Spec.HostNetwork = true
	}
	if spec.DNSPolicy != nil {
		worker.Spec.DNSPolicy = *spec.DNSPolicy
	}
	worker.Spec.Tolerations = spec.Tolerations
	worker.Spec.SchedulerName = spec.SchedulerName
	worker.Spec.ServiceAccountName = spec.ServiceAccountName
	worker.Spec.Volumes = spec.Volumes
	worker.Spec.Containers[0].Image = spec.Image
	worker.Spec.Containers[0].Name = common.WorkerContainerName
	worker.Spec.Containers[0].LivenessProbe = spec.LivenessProbe
	worker.Spec.Containers[0].ReadinessProbe = spec.ReadinessProbe
	worker.Spec.Containers[0].StartupProbe = spec.StartupProbe
	worker.Spec.Containers[0].VolumeMounts = spec.VolumeMounts
	worker.Spec.Containers[0].VolumeDevices = spec.VolumeDevices
	if spec.SecurityContext != nil {
		worker.Spec.Containers[0].SecurityContext = spec.SecurityContext
	} else {
		worker.Spec.Containers[0].SecurityContext = &corev1.SecurityContext{
			Privileged: utils.ToPtr(true),
		}
	}
	if spec.Lifecycle != nil {
		worker.Spec.Containers[0].Lifecycle = spec.Lifecycle
	} else {
		worker.Spec.Containers[0].Lifecycle = &corev1.Lifecycle{
			PreStop: &corev1.LifecycleHandler{
				Exec: &corev1.ExecAction{
					Command: []string{
						"sh",
						"-c",
						"umount " + common.MountPoint,
					},
				},
			},
		}
	}
	if spec.Resources != nil {
		worker.Spec.Containers[0].Resources = *spec.Resources
	} else {
		worker.Spec.Containers[0].Resources = common.DefaultResources
	}
	worker.Spec.Containers[0].Env = p.genEnvs()
	worker.Spec.Containers[0].Command = p.genCommands(ctx)
	return worker
}

func MergeCacheGrouopWorkerTemplate(template *juicefsiov1.CacheGroupWorkerTemplate, overwrite juicefsiov1.CacheGroupWorkerOverwrite) {
	if overwrite.ServiceAccountName != "" {
		template.ServiceAccountName = overwrite.ServiceAccountName
	}
	if overwrite.HostNetwork != nil {
		template.HostNetwork = overwrite.HostNetwork
	}
	if overwrite.SchedulerName != "" {
		template.SchedulerName = overwrite.SchedulerName
	}
	if overwrite.Tolerations != nil {
		template.Tolerations = overwrite.Tolerations
	}
	if overwrite.Image != "" {
		template.Image = overwrite.Image
	}
	if overwrite.Env != nil {
		template.Env = overwrite.Env
	}
	if overwrite.Resources != nil {
		template.Resources = overwrite.Resources
	}
	if overwrite.LivenessProbe != nil {
		template.LivenessProbe = overwrite.LivenessProbe
	}
	if overwrite.ReadinessProbe != nil {
		template.ReadinessProbe = overwrite.ReadinessProbe
	}
	if overwrite.StartupProbe != nil {
		template.StartupProbe = overwrite.StartupProbe
	}
	if overwrite.SecurityContext != nil {
		template.SecurityContext = overwrite.SecurityContext
	}
	if overwrite.Lifecycle != nil {
		template.Lifecycle = overwrite.Lifecycle
	}
	if overwrite.VolumeMounts != nil {
		template.VolumeMounts = overwrite.VolumeMounts
	}
	if overwrite.VolumeDevices != nil {
		template.VolumeDevices = overwrite.VolumeDevices
	}
	if overwrite.Volumes != nil {
		template.Volumes = overwrite.Volumes
	}
	if overwrite.Opts != nil {
		template.Opts = overwrite.Opts
	}
	if overwrite.DNSPolicy != nil {
		template.DNSPolicy = overwrite.DNSPolicy
	}
}
