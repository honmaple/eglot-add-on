package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/sourcegraph/jsonrpc2"
	"go.lsp.dev/protocol"
)

type ProcessServer struct {
	cmd    *exec.Cmd
	name   string
	stdin  io.ReadCloser
	stdout io.WriteCloser

	conn      *jsonrpc2.Conn
	proxyConn *jsonrpc2.Conn
}

func (s *ProcessServer) Name() string {
	return s.name
}

func (s *ProcessServer) Read(p []byte) (int, error) {
	return s.stdin.Read(p)
}

func (s *ProcessServer) Write(p []byte) (int, error) {
	return s.stdout.Write(p)
}

func (s *ProcessServer) Close() error {
	return errors.Join(s.stdin.Close(), s.stdout.Close(), s.cmd.Process.Kill())
}

func (s *ProcessServer) Call(ctx context.Context, method string, params *json.RawMessage, result any) error {
	return s.conn.Call(ctx, method, params, result)
}

func (s *ProcessServer) Notify(ctx context.Context, method string, params *json.RawMessage) error {
	return s.conn.Notify(ctx, method, params)
}

func (s *ProcessServer) Handle(ctx context.Context, conn *jsonrpc2.Conn, req *jsonrpc2.Request) {
	if req.Notif {
		s.proxyConn.Notify(ctx, req.Method, req.Params)
		return
	}

	result, err := s.handle(ctx, req)
	if err != nil {
		if err := conn.ReplyWithError(ctx, req.ID, &jsonrpc2.Error{
			Code:    jsonrpc2.CodeInternalError,
			Message: err.Error(),
		}); err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
		return
	}
	if err := conn.Reply(ctx, req.ID, result); err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
}

func (s *ProcessServer) handle(ctx context.Context, req *jsonrpc2.Request) (any, error) {
	if s.name == "tailwindcss-language-server" {
		if req.Method == protocol.MethodClientRegisterCapability {
			var newParams protocol.RegistrationParams
			if err := json.Unmarshal(*req.Params, &newParams); err != nil {
				return nil, err
			}
			for i := range newParams.Registrations {
				if newParams.Registrations[i].Method == protocol.MethodWorkspaceDidChangeWatchedFiles {
					newParams.Registrations[i].RegisterOptions = protocol.DidChangeWatchedFilesRegistrationOptions{
						Watchers: []protocol.FileSystemWatcher{
							{GlobPattern: "**/{tailwind,tailwind.config}.{js,cjs,ts,mjs}"},
							{GlobPattern: "**/{package-lock.json,yarn.lock,pnpm-lock.yaml}"},
							{GlobPattern: "**/*.{html,css,scss,sass,less,pcss}"},
						},
					}
				}
			}
			var result json.RawMessage

			if err := s.proxyConn.Call(ctx, req.Method, newParams, &result); err != nil {
				return nil, err
			}
			return result, nil
		}
	}

	var result json.RawMessage

	if err := s.proxyConn.Call(ctx, req.Method, req.Params, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func NewProcessServer(ctx context.Context, cmd *exec.Cmd) (*ProcessServer, error) {
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	proc := &ProcessServer{
		cmd:    cmd,
		name:   filepath.Base(cmd.Path),
		stdin:  stdout,
		stdout: stdin,
	}

	proc.conn = jsonrpc2.NewConn(ctx, jsonrpc2.NewBufferedStream(proc, jsonrpc2.VSCodeObjectCodec{}), proc)
	return proc, nil
}
