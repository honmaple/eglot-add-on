package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"

	"github.com/urfave/cli/v2"
)

func parseArgs(args []string) [][]string {
	cmds := make([][]string, 0)

	strs := make([]string, 0)
	for i := 0; i < len(args); i++ {
		if args[i] == "--" {
			if len(strs) > 0 {
				cmds = append(cmds, strs)
			}
			strs = make([]string, 0)
			continue
		}
		strs = append(strs, args[i])
	}
	if len(strs) > 0 {
		cmds = append(cmds, strs)
	}
	return cmds
}

func parseProvider(args []string) map[string][]string {
	result := make(map[string][]string)
	for _, c := range args {
		index := strings.Index(c, "=")
		if index == -1 {
			continue
		}
		result[c[:index]] = strings.Split(c[index+1:], ",")
	}
	return result
}

func main() {
	index := slices.Index(os.Args[1:], "--")

	app := &cli.App{
		Name: "lspx",
		Flags: []cli.Flag{
			&cli.StringSliceFlag{
				Name:    "provider",
				Aliases: []string{"p"},
				Usage:   "set provider with `key=value1,value2`",
			},
		},
		Action: func(clx *cli.Context) error {
			if index == -1 {
				return errors.New("not command found")
			}

			ctx := context.Background()

			procs := make([]*ProcessServer, 0)
			for _, command := range parseArgs(os.Args[index+1:]) {
				if len(command) >= 1 && command[0] == "" {
					continue
				}

				var cmd *exec.Cmd
				if len(command) == 1 {
					cmd = exec.CommandContext(ctx, command[0])
				} else {
					cmd = exec.CommandContext(ctx, command[0], command[1:]...)
				}

				proc, err := NewProcessServer(ctx, cmd)
				if err != nil {
					return err
				}
				defer proc.Close()

				procs = append(procs, proc)
			}

			proxy, err := NewProxyServer(ctx, procs, parseProvider(clx.StringSlice("provider")))
			if err != nil {
				return err
			}
			defer proxy.Close()

			return proxy.Wait()
		},
	}

	args := os.Args
	if index > -1 {
		args = os.Args[:index+1]
	}
	if err := app.Run(args); err != nil {
		fmt.Println(err.Error())
	}
}
