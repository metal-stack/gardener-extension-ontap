apiVersion: admissionregistration.k8s.io/v1
kind: MutatingWebhookConfiguration
metadata:
  name: gardener-extension-ontap-shoot
  labels:
    app.kubernetes.io/name: gardener-extension-ontap
    resources.gardener.cloud/managed-by: gardener
webhooks:
- name: shoot.ontap.extensions.gardener.cloud
  clientConfig:
    url: https://gardener-extension-extension-ontap.{{ .WebhookNamespace }}:443/shoot
    caBundle: {{ .CABundle }}
  rules:
  - operations: ["CREATE", "UPDATE"]
    apiGroups: ["apps"]
    apiVersions: ["v1"]
    resources: ["deployments"]
  - operations: ["CREATE", "UPDATE"]
    apiGroups: [""]
    apiVersions: ["v1"]
    resources: ["pods"]
  admissionReviewVersions: ["v1", "v1beta1"]
  sideEffects: None
  failurePolicy: Fail