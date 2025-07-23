apiVersion: metal-stack.io/v1
kind: ClusterwideNetworkPolicy
metadata:
  namespace: firewall
  name: allow-to-ontap
spec:
  egress:
  - to:
    - cidr: "{{ .ManagementLif }}/32"
    ports:
    - protocol: TCP
      port: 443
  - to:
    {{ range .DataLifs -}}
    - cidr: "{{ . }}/32"
    {{ end -}}
    ports:
    - protocol: TCP
      port: 4420
