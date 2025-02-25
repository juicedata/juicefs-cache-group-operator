/*
Copyright 2025 Juicedata Inc

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package builder

import (
	"context"
	"fmt"

	juicefsiov1 "github.com/juicedata/juicefs-cache-group-operator/api/v1"
	"github.com/juicedata/juicefs-cache-group-operator/pkg/common"
	"github.com/juicedata/juicefs-cache-group-operator/pkg/utils"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func genJuiceFSSecretData(juicefs *juicefsiov1.SyncSinkJuiceFS, suffix string) map[string]string {
	if juicefs == nil {
		return nil
	}
	data := make(map[string]string)
	if juicefs.Token.Value != "" {
		data[fmt.Sprintf("%s_TOKEN", suffix)] = juicefs.Token.Value
	}
	if juicefs.AccessKey.Value != "" {
		data[fmt.Sprintf("%s_ACCESS_KEY", suffix)] = juicefs.AccessKey.Value
	}
	if juicefs.SecretKey.Value != "" {
		data[fmt.Sprintf("%s_SECRET_KEY", suffix)] = juicefs.SecretKey.Value
	}
	return data
}

func genExternalSecretData(external *juicefsiov1.SyncSinkExternal, suffix string) map[string]string {
	if external == nil {
		return nil
	}
	data := make(map[string]string)
	if external.AccessKey.Value != "" {
		data[fmt.Sprintf("%s_ACCESS_KEY", suffix)] = external.AccessKey.Value
	}
	if external.SecretKey.Value != "" {
		data[fmt.Sprintf("%s_SECRET_KEY", suffix)] = external.SecretKey.Value
	}
	return data
}

func NewSyncSecret(ctx context.Context, sync *juicefsiov1.Sync) (*corev1.Secret, error) {
	secretName := common.GenSyncSecretName(sync.Name)
	data := make(map[string]string)

	if utils.IsDistributed(sync) {
		id_rsa, id_rsa_pub, err := utils.GenerateSSHKeyPair()
		if err != nil {
			return nil, err
		}
		data["id_rsa"] = id_rsa
		data["id_rsa.pub"] = id_rsa_pub
	}
	for k, b := range genJuiceFSSecretData(sync.Spec.From.JuiceFS, "JUICEFS_FROM") {
		data[k] = b
	}
	for k, b := range genJuiceFSSecretData(sync.Spec.To.JuiceFS, "JUICEFS_TO") {
		data[k] = b
	}
	for k, b := range genExternalSecretData(sync.Spec.From.External, "EXTERNAL_FROM") {
		data[k] = b
	}
	for k, b := range genExternalSecretData(sync.Spec.To.External, "EXTERNAL_TO") {
		data[k] = b
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: sync.Namespace,
		},
		StringData: data,
	}

	secret.SetOwnerReferences([]metav1.OwnerReference{
		{
			APIVersion: common.GroupVersion,
			Kind:       common.KindSync,
			Name:       sync.Name,
			UID:        sync.UID,
			Controller: utils.ToPtr(true),
		},
	})
	return secret, nil
}
