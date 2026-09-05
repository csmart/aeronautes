package rclone

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func BenchmarkGatewayStreaming(b *testing.B) {
	const size = 64 << 20
	for _, route := range []string{"direct", "gateway"} {
		b.Run(route, func(b *testing.B) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Length", fmt.Sprint(size))
				_, _ = io.CopyN(w, zeroStream{}, size)
			}))
			defer upstream.Close()
			remote := Remote{Name: "aeronautes_src", StorageURL: upstream.URL + gatewayAccount, Token: "benchmark-fixture"}
			if route == "gateway" {
				gateway, local, err := newStorageGateway(b.Context(), remote, nil, 64)
				if err != nil {
					b.Fatal(err)
				}
				defer gateway.Close()
				remote = local
			}
			b.SetBytes(size)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				req, err := http.NewRequestWithContext(b.Context(), "GET", remote.StorageURL+"/owned/object", nil)
				if err != nil {
					b.Fatal(err)
				}
				req.Header.Set("X-Auth-Token", remote.Token)
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					b.Fatal(err)
				}
				n, err := io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
				if err != nil || n != size || resp.StatusCode != 200 {
					b.Fatalf("stream bytes=%d status=%d err=%v", n, resp.StatusCode, err)
				}
			}
			b.StopTimer()
		})
	}
}
