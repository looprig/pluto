package packfile

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func TestYAMLNodeToJSON(t *testing.T) {
	var n yaml.Node
	src := "type: object\nproperties: {command: {type: string}}\nrequired: [command]\n"
	if err := yaml.Unmarshal([]byte(src), &n); err != nil {
		t.Fatal(err)
	}
	raw, err := jsonFromNode(&n)
	if err != nil {
		t.Fatalf("jsonFromNode: %v", err)
	}
	want := `{"properties":{"command":{"type":"string"}},"required":["command"],"type":"object"}`
	if string(raw) != want {
		t.Fatalf("got %s", raw)
	}
}

func TestYAMLNodeToJSONRejectsNonStringKeys(t *testing.T) {
	var n yaml.Node
	if err := yaml.Unmarshal([]byte("1: x\n"), &n); err != nil {
		t.Fatal(err)
	}
	if _, err := jsonFromNode(&n); err == nil {
		t.Fatal("non-string key accepted")
	}
}

func TestYAMLNodeToJSONRejectsAliases(t *testing.T) {
	var n yaml.Node
	if err := yaml.Unmarshal([]byte("a: &x hi\nb: *x\n"), &n); err != nil {
		t.Fatal(err)
	}
	if _, err := jsonFromNode(&n); err == nil {
		t.Fatal("yaml alias accepted")
	}
}
