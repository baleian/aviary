package authz

import (
	"encoding/base64"
	"testing"

	typev3 "github.com/envoyproxy/go-control-plane/envoy/type/v3"
	"github.com/google/uuid"

	"aviary/supervisor/internal/testenv"
)

func TestTheTokenOfASessionOpensToTheSession(t *testing.T) {
	keys := testenv.Keys(t)
	a := &authorizer{keys: keys}
	session := uuid.New()
	token, err := Token(keys, session)
	if err != nil {
		t.Fatal(err)
	}
	if opened, known := a.session(token); !known || opened != session {
		t.Errorf("the token of the session %s opens to %s, %t", session, opened, known)
	}
	// A value sealed for another place, and values that are not sealed.
	other, err := keys.Seal(session.String(), "the key of a provider")
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{base64.RawURLEncoding.EncodeToString(other), "a-token", ""} {
		if opened, known := a.session(token); known {
			t.Errorf("%q is taken for the token of the session %s", token, opened)
		}
	}
}

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
		basic("session:token"):  "token",
		basic("anyone:token"):   "token",
		basic(":token"):         "token",
		basic("session:to:ken"): "to:ken",
		basic("token"):          "",
		"Basic not base64":      "",
		"Bearer token":          "",
		"":                      "",
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
