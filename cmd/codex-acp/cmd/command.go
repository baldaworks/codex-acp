package command

import (
	"github.com/baldaworks/codex-acp/pkg/cobracmd"
	"github.com/spf13/cobra"
)

func Command() *cobra.Command {
	return cobracmd.New()
}
