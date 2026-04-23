package dockerproxy

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"time"
)

// hijackProxy handles Docker's attach/exec-start endpoints: after the initial
// HTTP request, both sides stream raw bytes (framed or stdin/stdout/stderr
// multiplexed via docker's stdcopy protocol). We dial upstream, forward the
// request verbatim, relay the response status line + headers raw (without
// trying to parse the body — the body IS the hijacked stream), then io.Copy
// in both directions until either half closes.
func (s *Server) hijackProxy(w http.ResponseWriter, r *http.Request) {
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, errHijackNotSupported.Error(), http.StatusInternalServerError)
		return
	}
	// Don't tie the dial to r.Context(): for hijacked endpoints, net/http cancels
	// the request context as soon as the client half-closes its write side
	// (which `docker run` without -i does almost immediately after sending
	// headers — see background-read in src/net/http/server.go). That would race
	// our DialContext and fail upstream handshake spuriously. The dial is a
	// local unix-socket round-trip; 5s standalone timeout is plenty.
	dialCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	upstream, err := dialer.DialContext(dialCtx, "unix", s.cfg.DockerSock)
	if err != nil {
		http.Error(w, "docker daemon unreachable", http.StatusBadGateway)
		return
	}
	defer upstream.Close()

	r2 := r.Clone(context.Background())
	r2.URL.Scheme = "http"
	r2.URL.Host = "docker"
	r2.RequestURI = ""
	// If upstream closed between Dial and Write, this errors; readRawHeaders
	// below will then hit EOF/read-error and surface the 502 uniformly.
	_ = r2.Write(upstream)

	// Read the raw upstream status line + headers into memory. Don't use
	// http.ReadResponse — it would parse a body, which on an upgrade/hijack
	// response is the raw stream itself (blocking forever).
	upReader := bufio.NewReader(upstream)
	headerBytes, err := readRawHeaders(upReader)
	if err != nil {
		http.Error(w, "read upstream: "+err.Error(), http.StatusBadGateway)
		return
	}

	// Hijack: converts the http.ResponseWriter into a raw net.Conn. For an
	// http.Server over a unix socket this always succeeds — it's only documented
	// to fail on HTTP/2 connections, which we don't negotiate here.
	client, clientBuf, _ := hj.Hijack()
	defer client.Close()

	// After hijack the client is a raw conn; Write/Flush errors here just
	// mean the client died mid-relay. There's no useful response to return,
	// so swallow and fall through to the io.Copy goroutines (which will then
	// exit on the broken-pipe error).
	_, _ = clientBuf.Write(headerBytes)
	_ = clientBuf.Flush()

	// Hard-stop deadline so wedged connections can't leak forever.
	_ = upstream.SetDeadline(time.Now().Add(24 * time.Hour))
	_ = client.SetDeadline(time.Now().Add(24 * time.Hour))

	// Wait for BOTH directions to finish. Waiting for only one causes the
	// other to be truncated when the deferred Close() runs — in particular,
	// for `docker run` (no -i), the client→upstream side finishes almost
	// immediately (no stdin), and returning here would cut off the
	// upstream→client stream before any container stdout/stderr reaches the
	// client. The 24h SetDeadline above is the belt-and-suspenders cap so a
	// wedged half can't pin the handler forever.
	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(upstream, clientBuf)
		closeWrite(upstream)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(client, upReader)
		closeWrite(client)
		done <- struct{}{}
	}()
	<-done
	<-done
}

// readRawHeaders reads from r up to and including the blank CRLF line that
// terminates an HTTP response's status line + headers. Returned bytes include
// the terminating CRLF so they can be relayed verbatim.
func readRawHeaders(r *bufio.Reader) ([]byte, error) {
	var buf []byte
	for {
		line, err := r.ReadSlice('\n')
		if err != nil {
			return nil, err
		}
		buf = append(buf, line...)
		if string(line) == "\r\n" || string(line) == "\n" {
			return buf, nil
		}
	}
}

// closeWrite does a half-close on a TCP/unix conn when possible; falls back to
// a full close. This matches what `docker exec` / `attach` expect on EOF.
func closeWrite(c net.Conn) {
	type halfCloser interface {
		CloseWrite() error
	}
	if hc, ok := c.(halfCloser); ok {
		_ = hc.CloseWrite()
		return
	}
	_ = c.Close()
}
