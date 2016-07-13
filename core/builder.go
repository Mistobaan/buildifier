package build

import (
	"bytes"
	"html/template"
	"log"
)

//NewBuildFile creates a BUILD
func NewBuildFile() (*File, error) {
	funcMap := template.FuncMap{}

	const templateText = `
{{.RuleName}}(
    {{range $index, $element := .Attrs}}
        "{{ $index }}" : [
           {{ range $name := $element }}
                "{{$name}}",
           {{end}}
        ],
    {{end}}
)
`
	tmpl, err := template.New("genrule").Funcs(funcMap).Parse(templateText)
	if err != nil {
		log.Fatalf("parsing: %s", err)
	}

	var b bytes.Buffer
	type M map[string]interface{}
	doc := M{
		"RuleName": "go_library",
		"Attrs": M{
			"srcs": []string{"src.go"},
		},
	}

	err = tmpl.Execute(&b, doc)
	if err != nil {
		log.Fatalf("execution: %s", err)
	}

	log.Println(b.String())

	return Parse("generated", b.Bytes())
}
