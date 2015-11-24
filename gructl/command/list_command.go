package command

import (
	"fmt"

	"github.com/codegangsta/cli"
	"github.com/gosuri/uitable"
)

func NewListCommand() cli.Command {
	cmd := cli.Command{
		Name:   "list",
		Usage:  "list registered minions",
		Action: execListCommand,
		Flags: []cli.Flag{
			cli.StringFlag{
				Name:  "with-classifier",
				Value: "",
				Usage: "match minions with given classifier pattern",
			},
		},
	}

	return cmd
}

// Executes the "list" command
func execListCommand(c *cli.Context) {
	client := newEtcdMinionClientFromFlags(c)

	cFlag := c.String("with-classifier")
	minions, err := parseClassifierPattern(client, cFlag)

	if err != nil {
		displayError(err, 1)
	}

	if len(minions) == 0 {
		return
	}

	table := uitable.New()
	table.MaxColWidth = 80
	table.AddRow("MINION", "NAME")
	for _, minion := range minions {
		name, err := client.MinionName(minion)
		if err != nil {
			displayError(err, 1)
		}

		table.AddRow(minion, name)
	}

	fmt.Println(table)
}
