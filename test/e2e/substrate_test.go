package e2e

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"testing"

	ateapi "go.ate.dev/substrate/api/ateapi/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
)

// The sandboxes of the sessions are actors of Agent Substrate, in the
// atespace of the chart. The tests read them through the API of Substrate, at
// a port of one of its pods that is forwarded to them, as the ServiceAccount
// ate-client, which the installation makes an owner of every atespace.
const (
	substrateNamespace = "ate-system"
	atespace           = "aviary"
)

// substrate returns a client of the API of Substrate.
var substrate = sync.OnceValues(func() (ateapi.ControlClient, error) {
	pods, err := cluster.CoreV1().Pods(substrateNamespace).List(ctx, metav1.ListOptions{LabelSelector: "app=ate-api-server"})
	if err != nil {
		return nil, err
	}
	var pod string
	for _, found := range pods.Items {
		if found.Status.Phase == corev1.PodRunning && found.DeletionTimestamp == nil {
			pod = found.Name
		}
	}
	if pod == "" {
		return nil, errors.New("no API server of Substrate runs")
	}
	transport, upgrader, err := spdy.RoundTripperFor(restConfig)
	if err != nil {
		return nil, err
	}
	at := cluster.CoreV1().RESTClient().Post().Resource("pods").Namespace(substrateNamespace).Name(pod).SubResource("portforward").URL()
	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, http.MethodPost, at)
	ready := make(chan struct{})
	forwarder, err := portforward.NewOnAddresses(dialer, []string{"127.0.0.1"}, []string{"0:443"}, make(chan struct{}), ready, io.Discard, os.Stderr)
	if err != nil {
		return nil, err
	}
	forwarded := make(chan error, 1)
	go func() { forwarded <- forwarder.ForwardPorts() }()
	select {
	case <-ready:
	case err := <-forwarded:
		return nil, fmt.Errorf("forward a port of %s: %w", pod, err)
	}
	ports, err := forwarder.GetPorts()
	if err != nil {
		return nil, err
	}

	// The API's certificate is signed by the service certificates of Substrate.
	bundles, err := cluster.CertificatesV1().ClusterTrustBundles().List(ctx, metav1.ListOptions{LabelSelector: "podcert.ate.dev/canarying=live"})
	if err != nil {
		return nil, err
	}
	authorities := x509.NewCertPool()
	for _, bundle := range bundles.Items {
		if bundle.Spec.SignerName == "servicedns.podcert.ate.dev/identity" {
			authorities.AppendCertsFromPEM([]byte(bundle.Spec.TrustBundle))
		}
	}
	token, err := cluster.CoreV1().ServiceAccounts(substrateNamespace).CreateToken(ctx, "ate-client", &authenticationv1.TokenRequest{
		Spec: authenticationv1.TokenRequestSpec{Audiences: []string{"api." + substrateNamespace + ".svc"}},
	}, metav1.CreateOptions{})
	if err != nil {
		return nil, err
	}
	conn, err := grpc.NewClient(fmt.Sprintf("127.0.0.1:%d", ports[0].Local),
		grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{RootCAs: authorities, ServerName: "api." + substrateNamespace + ".svc"})),
		grpc.WithPerRPCCredentials(bearer(token.Status.Token)))
	if err != nil {
		return nil, err
	}
	return ateapi.NewControlClient(conn), nil
})

// bearer sends a token with every call.
type bearer string

func (b bearer) GetRequestMetadata(context.Context, ...string) (map[string]string, error) {
	return map[string]string{"authorization": "Bearer " + string(b)}, nil
}

func (bearer) RequireTransportSecurity() bool { return true }

// control returns the client of the API of Substrate, or fails the test.
func control(t *testing.T) ateapi.ControlClient {
	t.Helper()
	client, err := substrate()
	if err != nil {
		t.Fatalf("connect to the API of Substrate: %v", err)
	}
	return client
}

// actor is what the tests read of the sandbox of a session.
type actor struct {
	UID   string
	State ateapi.ActorState
}

// actorOf returns the sandbox of the session, or nil if it has none.
func actorOf(t *testing.T, session string) *actor {
	t.Helper()
	found, err := control(t).GetActor(ctx, &ateapi.GetActorRequest{Actor: &ateapi.ObjectRef{Atespace: atespace, Name: "session-" + session}})
	if status.Code(err) == codes.NotFound {
		return nil
	}
	if err != nil {
		t.Fatalf("get the sandbox of session %s: %v", session, err)
	}
	if found.GetMetadata().GetDeleteTime() != nil {
		return nil
	}
	return &actor{UID: found.GetMetadata().GetUid(), State: found.GetStatus().GetState()}
}

// sandboxCount returns the number of sandboxes of sessions.
func sandboxCount(t *testing.T) int {
	t.Helper()
	count, page := 0, ""
	for {
		listed, err := control(t).ListActors(ctx, &ateapi.ListActorsRequest{Atespace: atespace, Options: &ateapi.ListOptions{PageToken: page}})
		if status.Code(err) == codes.NotFound {
			return 0
		}
		if err != nil {
			t.Fatalf("list the sandboxes: %v", err)
		}
		for _, found := range listed.GetActors() {
			if found.GetMetadata().GetDeleteTime() == nil {
				count++
			}
		}
		if page = listed.GetNextPageToken(); page == "" {
			return count
		}
	}
}

// deleteSandbox deletes the sandbox of the session, as if something else had.
// Substrate refuses it while another operation on the sandbox is in progress,
// as when the Supervisors put it to sleep to make room for other turns.
func deleteSandbox(t *testing.T, session string) {
	t.Helper()
	var err error
	eventually(t, soon, "the sandbox of session "+session+" is still busy", func() bool {
		_, err = control(t).DeleteActor(ctx, &ateapi.DeleteActorRequest{Actor: &ateapi.ObjectRef{Atespace: atespace, Name: "session-" + session}})
		return status.Code(err) != codes.Aborted
	})
	if err != nil {
		t.Fatalf("delete the sandbox of session %s: %v", session, err)
	}
}

// asleep waits until the Supervisors have put the session's sandbox to sleep.
func asleep(t *testing.T, session string) {
	t.Helper()
	eventually(t, patience, "session "+session+" is not asleep", func() bool {
		found := actorOf(t, session)
		return found != nil && found.State == ateapi.ActorState_ACTOR_STATE_SUSPENDED
	})
}
