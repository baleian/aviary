package gateway

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// call is the context of a call that came with the metadata, given as pairs
// of a name and a value.
func call(pairs ...string) context.Context {
	return metadata.NewIncomingContext(context.Background(), metadata.Pairs(pairs...))
}

func TestRunnerID(t *testing.T) {
	const id = "0b0e3c5e-6f0d-4c8e-9d43-3c3f6f2f8a11"
	if runner, err := runnerID(call("aviary-runner", id)); err != nil || runner == nil || runner.String() != id {
		t.Errorf("the ID of a runner is %v, %v", runner, err)
	}
	// A runner of an older release.
	if runner, err := runnerID(call()); err != nil || runner != nil {
		t.Errorf("the ID of a runner that sent none is %v, %v", runner, err)
	}
	for name, ctx := range map[string]context.Context{
		"not a UUID": call("aviary-runner", "runner-1"),
		"two IDs":    call("aviary-runner", id, "aviary-runner", id),
	} {
		if runner, err := runnerID(ctx); status.Code(err) != codes.InvalidArgument {
			t.Errorf("%s: the ID is %v, %v", name, runner, err)
		}
	}
}

func TestBearerToken(t *testing.T) {
	if token, err := bearerToken(call("authorization", "Bearer token")); err != nil || token != "token" {
		t.Errorf("the token is %q, %v", token, err)
	}
	for name, ctx := range map[string]context.Context{
		"no token":     call(),
		"an empty one": call("authorization", "Bearer "),
		"another kind": call("authorization", "Basic token"),
		"only a token": call("authorization", "token"),
		"two of them":  call("authorization", "Bearer token", "authorization", "Bearer token"),
	} {
		if token, err := bearerToken(ctx); status.Code(err) != codes.Unauthenticated {
			t.Errorf("%s: the token is %q, %v", name, token, err)
		}
	}
}
