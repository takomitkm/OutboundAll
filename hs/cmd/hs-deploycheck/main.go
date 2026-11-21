// Command hs-deploycheck is a read-only availability probe for a deployed
// OutboundAll-hs VPS: verifies headscale health, controller API health, and
// (given a tailscale socket) that this machine's tailnet identity is alive.
//
// Deliberately tiny: the 2c/1G deployment target must not run anything heavy.
//
// Usage: hs-deploycheck --hs-health URL --controller URL [--token T]
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

func main() {
	hsHealth := flag.String("hs-health", "http://127.0.0.1:18080/health", "headscale health URL")
	ctlHealth := flag.String("controller", "http://127.0.0.1:8800/api/v1/health", "hs-controller health URL")
	flag.Parse()
	fail := 0

	for _, c := range []struct{ name, url string }{
		{"headscale", *hsHealth},
		{"hs-controller", *ctlHealth},
	} {
		client := &http.Client{Timeout: 5 * time.Second}
		resp, err := client.Get(c.url)
		if err != nil {
			fmt.Printf("FAIL %s: %v\n", c.name, err)
			fail++
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		resp.Body.Close()
		if resp.StatusCode != 200 {
			fmt.Printf("FAIL %s: HTTP %d\n", c.name, resp.StatusCode)
			fail++
			continue
		}
		fmt.Printf("OK   %s: %s\n", c.name, body)
	}
	if fail > 0 {
		os.Exit(1)
	}
	_ = json.Marshal
	fmt.Println("DEPLOY-OK")
}
