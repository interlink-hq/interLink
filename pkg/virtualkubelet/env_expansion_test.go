package virtualkubelet

import (
	"testing"

	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
)

const (
	expandVarName  = "JOB_NAME"
	expandVarRef   = "$(" + expandVarName + ")"
	expandVarValue = "job-1"
)

func TestExpandEnvReferences(t *testing.T) {
	vars := map[string]string{
		expandVarName: expandVarValue,
		"EMPTY":       "",
		"NESTED":      expandVarRef,
	}

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "no reference", input: "plain", want: "plain"},
		{name: "reference", input: "--name=" + expandVarRef, want: "--name=" + expandVarValue},
		{name: "several references", input: expandVarRef + "-" + expandVarRef, want: expandVarValue + "-" + expandVarValue},
		{name: "empty value", input: "a$(EMPTY)b", want: "ab"},
		{name: "value is not expanded again", input: "$(NESTED)", want: expandVarRef},
		{name: "unknown variable", input: "$(UNKNOWN)", want: "$(UNKNOWN)"},
		{name: "escaped reference", input: "$" + expandVarRef, want: expandVarRef},
		{name: "escaped dollar", input: "cost $$5", want: "cost $5"},
		{name: "escape then reference", input: "$$" + expandVarRef, want: "$" + expandVarValue},
		{name: "unclosed reference", input: "$(" + expandVarName, want: "$(" + expandVarName},
		{name: "unclosed then reference", input: "$(" + expandVarRef, want: "$(" + expandVarRef},
		{name: "dollar without parenthesis", input: "$" + expandVarName, want: "$" + expandVarName},
		{name: "trailing dollar", input: "end$", want: "end$"},
		{name: "shell syntax untouched", input: "${" + expandVarName + "}", want: "${" + expandVarName + "}"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, expandEnvReferences(tt.input, vars))
		})
	}
}

func TestExpandContainerCommandAndArgs(t *testing.T) {
	container := v1.Container{
		Name:    "main",
		Command: []string{"/bin/run", "--job=" + expandVarRef},
		Args:    []string{"--ns=$(JOB_NAMESPACE)", "--secret=$(FROM_SECRET)", "$" + expandVarRef, "--last=$(DUP)"},
		Env: []v1.EnvVar{
			{Name: expandVarName, Value: expandVarValue},
			{Name: "JOB_NAMESPACE", Value: "batch"},
			{Name: "FROM_SECRET", ValueFrom: &v1.EnvVarSource{SecretKeyRef: &v1.SecretKeySelector{Key: "k"}}},
			{Name: "DUP", Value: "first"},
			{Name: "DUP", Value: "second"},
		},
	}
	original := container.Args

	expandContainerCommandAndArgs(&container)

	assert.Equal(t, "--ns=$(JOB_NAMESPACE)", original[0], "the original args must not change")
	assert.Equal(t, []string{"/bin/run", "--job=" + expandVarValue}, container.Command)
	assert.Equal(t, []string{"--ns=batch", "--secret=$(FROM_SECRET)", expandVarRef, "--last=second"}, container.Args)
}
