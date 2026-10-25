package api

import (
	"encoding/json"
	"testing"
)

func TestEnvItems(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		item DeployEnvItem
		want string
	}{
		{"plain", EnvVarItem("LOG_LEVEL", "info"), `{"key":"LOG_LEVEL","kind":"env","value":"info"}`},
		{"secret", SecretRefItem("DB_URL"), `{"key":"DB_URL","kind":"secret"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			b, err := json.Marshal(tt.item)
			if err != nil {
				t.Fatal(err)
			}
			if string(b) != tt.want {
				t.Errorf("got %s, want %s", b, tt.want)
			}
		})
	}
}

func TestEnvItemValue(t *testing.T) {
	t.Parallel()

	v, s, err := EnvItemValue(EnvVarItem("A", "1"))
	if err != nil || v == nil || s != nil || v.Key != "A" || v.Value != "1" {
		t.Errorf("env var: %+v %+v %v", v, s, err)
	}
	v, s, err = EnvItemValue(SecretRefItem("B"))
	if err != nil || v != nil || s == nil || s.Key != "B" {
		t.Errorf("secret ref: %+v %+v %v", v, s, err)
	}
	var unknown DeployEnvItem
	if err := unknown.UnmarshalJSON([]byte(`{"kind":"file","key":"C"}`)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := EnvItemValue(unknown); err == nil {
		t.Error("unknown kind should fail")
	}
}

// The generated helper writes the schema name as the kind. If this starts
// failing, the platform spec gained a discriminator mapping and the
// helpers in envitem.go can go.
func TestGeneratedDiscriminatorStillBroken(t *testing.T) {
	t.Parallel()

	var item DeployEnvItem
	if err := item.FromDeployEnvVar(DeployEnvVar{Key: "A", Value: "1"}); err != nil {
		t.Fatal(err)
	}
	kind, err := item.Discriminator()
	if err != nil {
		t.Fatal(err)
	}
	if kind != "DeployEnvVar" {
		t.Errorf("generated kind = %q; the spec may now have a mapping, see envitem.go", kind)
	}
}
