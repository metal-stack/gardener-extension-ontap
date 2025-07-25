package secrets

import (
	"bytes"
	_ "embed"
	"text/template"
)

//go:embed svm-shoot-secret.yaml.tpl
var secretsTemplate string

type Secrets struct {
	Name      string
	Namespace string
	Project   string
	Username  string
	Password  string
}

func Parse(secrets Secrets) (string, error) {
	tmpl := template.Must(template.New("secrets").Parse(string(secretsTemplate)))
	var result bytes.Buffer

	err := tmpl.Execute(&result, secrets)
	if err != nil {
		return "", err
	}
	return result.String(), nil
}
