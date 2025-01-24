package services

import (
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func BuildTridentSecret(secretName string, userName string, password string, projectId string) *corev1.Secret {

	secret := &corev1.Secret{
		ObjectMeta: v1.ObjectMeta{
			Name:      secretName,
			Namespace: "trident",
		},
		Data: map[string][]byte{
			"username":  []byte(userName),
			"password":  []byte(password),
			"projectId": []byte(projectId),
		},
	}

	return secret
}

// func resourceQuantity(s string) resource.Quantity {
// 	q, _ := resource.ParseQuantity(s)
// 	return q
// }
