package authz

import (
	"encoding/base64"
	"testing"

	typev3 "github.com/envoyproxy/go-control-plane/envoy/type/v3"
)

func TestBearerToken(t *testing.T) {
	for header, token := range map[string]string{
		"Bearer token": "token",
		"Bearer ":      "",
		"token":        "",
		"Basic token":  "",
		"":             "",
	} {
		if got := bearerToken(header); got != token {
			t.Errorf("bearerToken(%q) is %q, not %q", header, got, token)
		}
	}
}

func TestProxyPassword(t *testing.T) {
	basic := func(credentials string) string {
		return "Basic " + base64.StdEncoding.EncodeToString([]byte(credentials))
	}
	for header, password := range map[string]string{
		basic("turn:token"):   "token",
		basic("anyone:token"): "token",
		basic(":token"):       "token",
		basic("turn:to:ken"):  "to:ken",
		basic("token"):        "",
		"Basic not base64":    "",
		"Bearer token":        "",
		"":                    "",
	} {
		if got := proxyPassword(header); got != password {
			t.Errorf("proxyPassword(%q) is %q, not %q", header, got, password)
		}
	}
}

func TestDenied(t *testing.T) {
	llm := denied(false).GetDeniedResponse()
	if llm.GetStatus().GetCode() != typev3.StatusCode_Unauthorized || len(llm.GetHeaders()) != 0 {
		t.Errorf("a model request is refused with %v", llm)
	}

	// Without the header, a client that sends its password only when asked
	// takes the refusal as final. git is one.
	forward := denied(true).GetDeniedResponse()
	if forward.GetStatus().GetCode() != typev3.StatusCode_ProxyAuthenticationRequired {
		t.Errorf("a request to the forward proxy is refused with the status %v", forward.GetStatus().GetCode())
	}
	var asks bool
	for _, header := range forward.GetHeaders() {
		if header.GetHeader().GetKey() == "Proxy-Authenticate" && header.GetHeader().GetValue() == `Basic realm="aviary"` {
			asks = true
		}
	}
	if !asks {
		t.Errorf("the refusal does not ask for the password of the proxy: %v", forward.GetHeaders())
	}
}
