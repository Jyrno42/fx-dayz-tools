package cli

import (
	"io"

	"gopkg.in/yaml.v3"
)

// writeYAMLTo renders a value as YAML for `config show` and `--print`.
func writeYAMLTo(w io.Writer, v any) error {
	enc := yaml.NewEncoder(w)
	enc.SetIndent(2)
	if err := enc.Encode(v); err != nil {
		return coded(ExitInternal, "encode: %v", err)
	}
	return enc.Close()
}
