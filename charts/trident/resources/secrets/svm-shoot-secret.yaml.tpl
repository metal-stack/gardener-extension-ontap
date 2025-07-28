apiVersion: v1
kind: Secret
metadata:
  name: "{{ .Name }}"
  namespace: "{{ .Namespace }}"
  labels:
    app.kubernetes.io/part-of: gardener-extension-ontap
    app.kubernetes.io/managed-by: gardener
    ontap.metal-stack.io/project-id: "{{ .Project }}"
type: Opaque
stringData:
  username: "{{ .Username }}"
  password: "{{ .Password }}"
