package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/sourcegraph/jsonrpc2"
	"go.lsp.dev/protocol"
	"golang.org/x/sync/errgroup"
)

type stdrwc struct{}

func (stdrwc) Read(p []byte) (int, error) {
	return os.Stdin.Read(p)
}

func (c stdrwc) Write(p []byte) (int, error) {
	return os.Stdout.Write(p)
}

func (c stdrwc) Close() error {
	return errors.Join(os.Stdin.Close(), os.Stdout.Close())
}

type ProxyServer struct {
	conn  *jsonrpc2.Conn
	procs []*ProcessServer
}

func handleProcs[T any](ctx context.Context, req *jsonrpc2.Request, procs []*ProcessServer) ([]T, error) {
	eg := errgroup.Group{}

	results := make([]T, len(procs))
	for i, proc := range procs {
		index := i
		newProc := proc

		eg.Go(func() error {
			var result T

			if err := newProc.Call(ctx, req.Method, req.Params, &result); err != nil {
				return err
			}
			results[index] = result
			return nil
		})
	}
	if err := eg.Wait(); err != nil {
		return nil, err
	}
	return results, nil
}

func (s *ProxyServer) Wait() error {
	<-s.conn.DisconnectNotify()
	return nil
}

func (s *ProxyServer) Close() error {
	return s.conn.Close()
}

func (s *ProxyServer) Handle(ctx context.Context, conn *jsonrpc2.Conn, req *jsonrpc2.Request) {
	if req.Notif {
		if err := s.handleNotify(ctx, req); err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
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

func (s *ProxyServer) handleNotify(ctx context.Context, req *jsonrpc2.Request) error {
	eg := errgroup.Group{}

	for _, proc := range s.procs {
		newProc := proc

		eg.Go(func() error {
			return newProc.Notify(ctx, req.Method, req.Params)
		})
	}
	return eg.Wait()
}

func (s *ProxyServer) handle(ctx context.Context, req *jsonrpc2.Request) (any, error) {
	switch req.Method {
	case protocol.MethodInitialize:
		return s.handleInitialize(ctx, req)
	case protocol.MethodTextDocumentCompletion:
		return s.handleTextDocumentCompletion(ctx, req)
	// case protocol.MethodTextDocumentPublishDiagnostics:
	//	return s.handleTextDocumentPublishDiagnostics(ctx, req)
	default:
		results, err := handleProcs[json.RawMessage](ctx, req, s.procs)
		if err != nil {
			return nil, err
		}
		return results[0], nil
	}
}

func (s *ProxyServer) handleInitialize(ctx context.Context, req *jsonrpc2.Request) (any, error) {
	results, err := handleProcs[protocol.InitializeResult](ctx, req, s.procs)
	if err != nil {
		return nil, err
	}

	inititalize := protocol.InitializeResult{
		ServerInfo:   results[0].ServerInfo,
		Capabilities: results[0].Capabilities,
	}

	for _, result := range results[1:] {
		newCapabilities := merge(&inititalize.Capabilities, &result.Capabilities)

		c, ok := newCapabilities.(*protocol.ServerCapabilities)
		if ok {
			inititalize.Capabilities = *c
		}
	}
	return inititalize, nil
}

func (s *ProxyServer) handleTextDocumentCompletion(ctx context.Context, req *jsonrpc2.Request) (any, error) {
	results, err := handleProcs[protocol.CompletionList](ctx, req, s.procs)
	if err != nil {
		return nil, err
	}

	completion := protocol.CompletionList{
		Items:        make([]protocol.CompletionItem, 0),
		IsIncomplete: false,
	}
	for _, result := range results {
		if result.IsIncomplete {
			completion.IsIncomplete = true
			continue
		}
		completion.Items = append(completion.Items, result.Items...)
	}
	return completion, nil
}

// func (s *ProxyServer) handleTextDocumentPublishDiagnostics(ctx context.Context, req *jsonrpc2.Request) (any, error) {
//	results, err := handleProcs[protocol.PublishDiagnosticsParams](ctx, req, s.procs)
//	if err != nil {
//		return nil, err
//	}
//	return results[0], nil
// }

func NewProxyServer(ctx context.Context, procs []*ProcessServer) (*ProxyServer, error) {
	proxy := &ProxyServer{
		procs: procs,
	}
	proxy.conn = jsonrpc2.NewConn(ctx, jsonrpc2.NewBufferedStream(stdrwc{}, jsonrpc2.VSCodeObjectCodec{}), proxy)
	for _, proc := range procs {
		proc.proxyConn = proxy.conn
	}
	return proxy, nil
}
