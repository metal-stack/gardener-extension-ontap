package cwnps

import (
	"bytes"
	_ "embed"
	"text/template"
)

//go:embed cwnp.yaml.tpl
var cwnpTemplate string

type CWNP struct {
	ManagementLif string
	DataLifs      []string
}

func ParseCWNP(cwnp CWNP) (string, error) {
	tmpl := template.Must(template.New("cwnp").Parse(string(cwnpTemplate)))
	var result bytes.Buffer

	err := tmpl.Execute(&result, cwnp)
	if err != nil {
		return "", err
	}
	return result.String(), nil
}
