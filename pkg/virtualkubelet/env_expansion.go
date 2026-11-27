package virtualkubelet

import (
	"strings"

	v1 "k8s.io/api/core/v1"
)

// expandContainerCommandAndArgs replaces the $(VAR_NAME) references in the command and
// args of a container with the values of its environment variables, like the kubelet
// does before it starts a container. It runs after the environment is resolved, so only
// variables with a plain value are used; references to unknown or unresolved variables
// are left as they are, and $$ escapes a reference.
func expandContainerCommandAndArgs(container *v1.Container) {
	if len(container.Command) == 0 && len(container.Args) == 0 {
		return
	}

	vars := make(map[string]string, len(container.Env))
	for _, env := range container.Env {
		if env.ValueFrom == nil {
			vars[env.Name] = env.Value
		}
	}

	// New slices, since the offloaded containers are shallow copies of the pod's.
	container.Command = expandEnvReferencesInList(container.Command, vars)
	container.Args = expandEnvReferencesInList(container.Args, vars)
}

func expandEnvReferencesInList(list []string, vars map[string]string) []string {
	if list == nil {
		return nil
	}
	expanded := make([]string, len(list))
	for i, item := range list {
		expanded[i] = expandEnvReferences(item, vars)
	}
	return expanded
}

// expandEnvReferences expands the $(VAR_NAME) references in input with the Kubernetes
// rules: $$ becomes $, a reference to a variable that is not in vars and an unclosed
// $( are kept as they are, and a $ followed by anything else is copied.
func expandEnvReferences(input string, vars map[string]string) string {
	if !strings.Contains(input, "$") {
		return input
	}

	var b strings.Builder
	for i := 0; i < len(input); i++ {
		if input[i] != '$' || i+1 == len(input) {
			b.WriteByte(input[i])
			continue
		}

		switch input[i+1] {
		case '$':
			b.WriteByte('$')
			i++
		case '(':
			end := strings.IndexByte(input[i+2:], ')')
			if end < 0 {
				b.WriteString("$(")
				i++
				continue
			}
			name := input[i+2 : i+2+end]
			if value, ok := vars[name]; ok {
				b.WriteString(value)
			} else {
				b.WriteString("$(" + name + ")")
			}
			i += 2 + end
		default:
			b.WriteByte('$')
		}
	}
	return b.String()
}
