package api

import (
	"encoding/json"
	"fmt"

	"github.com/devlyft/cli/internal/api/gen/deploys"
)

// The platform spec's DeployEnvItem discriminator has no mapping, so the
// generated FromDeployEnvVar, FromDeploySecretRef and ValueByDiscriminator
// use schema names ("DeployEnvVar") as the kind instead of "env" and
// "secret", which the API rejects. Build and read env items with these
// helpers instead.

// EnvVarItem returns an env entry with a plain value.
func EnvVarItem(key, value string) DeployEnvItem {
	return envItem(DeployEnvVar{Kind: deploys.Env, Key: key, Value: value})
}

// SecretRefItem returns an env entry that takes the value of the
// environment secret named key; the variable's name equals the key.
func SecretRefItem(key string) DeployEnvItem {
	return envItem(DeploySecretRef{Kind: deploys.Secret, Key: key})
}

func envItem(v any) DeployEnvItem {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err) // plain structs of strings always marshal
	}
	var item DeployEnvItem
	if err := item.UnmarshalJSON(b); err != nil {
		panic(err)
	}
	return item
}

// EnvItemValue returns the env entry as exactly one of a plain variable or
// a secret reference.
func EnvItemValue(item DeployEnvItem) (*DeployEnvVar, *DeploySecretRef, error) {
	kind, err := item.Discriminator()
	if err != nil {
		return nil, nil, err
	}
	switch kind {
	case string(deploys.Env):
		v, err := item.AsDeployEnvVar()
		return &v, nil, err
	case string(deploys.Secret):
		s, err := item.AsDeploySecretRef()
		return nil, &s, err
	}
	return nil, nil, fmt.Errorf("unknown env entry kind %q", kind)
}
