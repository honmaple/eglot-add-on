package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"sync"

	"github.com/bytedance/sonic"
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
	conn      *jsonrpc2.Conn
	procs     []*ProcessServer
	providers map[string][]string

	diagMu    sync.Mutex
	diagCache map[protocol.DocumentURI]map[string][]protocol.Diagnostic
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

func (s *ProxyServer) getProcIndex(conn *jsonrpc2.Conn) int {
	for i, proc := range s.procs {
		if proc.conn == conn {
			return i
		}
	}
	return -1
}

func (s *ProxyServer) Handle(ctx context.Context, conn *jsonrpc2.Conn, req *jsonrpc2.Request) {
	if req.Notif {
		procIdx := s.getProcIndex(conn)
		if procIdx != -1 {
			if err := s.handleNotify(ctx, req, s.procs[procIdx]); err != nil {
				fmt.Fprintln(os.Stderr, err)
			}
			return
		}
		eg := errgroup.Group{}

		for _, proc := range s.procs {
			newProc := proc

			eg.Go(func() error {
				return newProc.Notify(ctx, req.Method, req.Params)
			})
		}
		if err := eg.Wait(); err != nil {
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

func (s *ProxyServer) handleNotify(ctx context.Context, req *jsonrpc2.Request, proc *ProcessServer) error {
	var params any = req.Params

	switch req.Method {
	case protocol.MethodTextDocumentPublishDiagnostics:
		newParams, err := s.handleTextDocumentPublishDiagnostics(req, proc)
		if err != nil {
			return err
		}
		params = newParams
	}
	return s.conn.Notify(ctx, req.Method, params)
}

func (s *ProxyServer) handleTextDocumentPublishDiagnostics(req *jsonrpc2.Request, proc *ProcessServer) (any, error) {
	var params protocol.PublishDiagnosticsParams
	if err := sonic.Unmarshal(*req.Params, &params); err != nil {
		return nil, err
	}

	s.diagMu.Lock()
	defer s.diagMu.Unlock()

	if s.diagCache[params.URI] == nil {
		s.diagCache[params.URI] = make(map[string][]protocol.Diagnostic)
	}

	sourceTag := proc.Name()
	for i := range params.Diagnostics {
		if params.Diagnostics[i].Source == "" {
			params.Diagnostics[i].Source = sourceTag
		}
	}
	s.diagCache[params.URI][sourceTag] = params.Diagnostics

	mergedDiagnostics := []protocol.Diagnostic{}
	for _, diags := range s.diagCache[params.URI] {
		mergedDiagnostics = append(mergedDiagnostics, diags...)
	}

	return protocol.PublishDiagnosticsParams{
		URI:         params.URI,
		Diagnostics: mergedDiagnostics,
	}, nil
}

func (s *ProxyServer) getProcsByProviders(method string) []*ProcessServer {
	providerMap := map[string]string{
		protocol.MethodTextDocumentHover:      "hover",
		protocol.MethodTextDocumentCompletion: "completion",
		protocol.MethodTextDocumentDefinition: "definition",
		protocol.MethodTextDocumentRename:     "rename",
		protocol.MethodTextDocumentReferences: "references",
	}

	m, ok := providerMap[method]
	if !ok {
		return s.procs
	}
	providers, ok := s.providers[m]
	if !ok || len(providers) == 0 {
		return s.procs
	}

	procs := make([]*ProcessServer, 0)
	for _, proc := range s.procs {
		if slices.Contains(providers, proc.Name()) {
			procs = append(procs, proc)
		}
	}
	if len(procs) == 0 {
		return s.procs
	}
	return procs
}

func (s *ProxyServer) handle(ctx context.Context, req *jsonrpc2.Request) (any, error) {
	procs := s.getProcsByProviders(req.Method)

	switch req.Method {
	case protocol.MethodInitialize:
		return s.handleInitialize(ctx, req, s.procs)
	case protocol.MethodTextDocumentCompletion:
		return s.handleTextDocumentCompletion(ctx, req, procs)
	default:
		results, err := handleProcs[json.RawMessage](ctx, req, procs)
		if err != nil {
			return nil, err
		}
		return results[0], nil
	}
}

func (s *ProxyServer) handleInitialize(ctx context.Context, req *jsonrpc2.Request, procs []*ProcessServer) (any, error) {
	results, err := handleProcs[protocol.InitializeResult](ctx, req, procs)
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

func (s *ProxyServer) handleTextDocumentCompletion(ctx context.Context, req *jsonrpc2.Request, procs []*ProcessServer) (any, error) {
	results, err := handleProcs[protocol.CompletionList](ctx, req, procs)
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

func NewProxyServer(ctx context.Context, procs []*ProcessServer, providers map[string][]string) (*ProxyServer, error) {
	proxy := &ProxyServer{
		procs:     procs,
		providers: providers,
		diagCache: make(map[protocol.DocumentURI]map[string][]protocol.Diagnostic),
	}
	proxy.conn = jsonrpc2.NewConn(ctx, jsonrpc2.NewBufferedStream(stdrwc{}, VSCodeObjectCodec{}), proxy)
	for _, proc := range procs {
		proc.proxyConn = proxy.conn
	}
	return proxy, nil
}
