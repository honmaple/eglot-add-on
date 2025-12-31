package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"slices"

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
			strs = strs[:0]
			continue
		}
		strs = append(strs, args[i])
	}
	return cmds
}

func main() {
	index := slices.Index(os.Args[1:], "--")
	if index == -1 {
		log.Fatalln("not command found")
	}

	app := &cli.App{
		Name: "lspx",
		Action: func(clx *cli.Context) error {
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

			proxy, err := NewProxyServer(ctx, procs)
			if err != nil {
				return err
			}
			defer proxy.Close()

			return proxy.Wait()
		},
	}
	if err := app.Run(os.Args[:index]); err != nil {
		fmt.Println(err.Error())
	}
}
