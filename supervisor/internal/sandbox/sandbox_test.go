package sandbox

import (
	"context"
	"errors"
	"net"
	"net/url"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8stesting "k8s.io/client-go/testing"
	agents "sigs.k8s.io/agent-sandbox/api/v1beta1"
	"sigs.k8s.io/agent-sandbox/clients/k8s/clientset/versioned/fake"
	typed "sigs.k8s.io/agent-sandbox/clients/k8s/clientset/versioned/typed/api/v1beta1"
)

var ctx = context.Background()

// cluster returns a client of sandboxes in a cluster of a test, and the
// sandboxes of the cluster as they are kept there. The Egress Proxy of the
// cluster is on this machine.
func cluster(t *testing.T, settings Settings) (*Client, typed.SandboxInterface) {
	t.Helper()
	settings.Namespace = "sessions"
	settings.Proxy = &url.URL{Scheme: "http", Host: "localhost:3128"}
	settings.Models = &url.URL{Scheme: "http", Host: "localhost:8080"}
	sandboxes := fake.NewSimpleClientset().AgentsV1beta1().Sandboxes(settings.Namespace)
	return &Client{sandboxes: sandboxes, settings: settings}, sandboxes
}

var opencode = Pod{
	Harness: Harness{
		Image: "harness-opencode:1",
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("250m"), corev1.ResourceMemory: resource.MustParse("1Gi")},
			Limits:   corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("4Gi")},
		},
	},
	Token: "the-token-of-the-session",
}

func TestTheSandboxOfASessionIsMadeWithItsVolume(t *testing.T) {
	c, sandboxes := cluster(t, Settings{Volume: resource.MustParse("10Gi"), StorageClass: "fast", MaxToolOutputBytes: 100})
	session := uuid.New()
	request := Request{Session: session, Labels: map[string]string{"aviary.io/agent": "an-agent"}, Pod: opencode}
	if err := c.Create(ctx, request); err != nil {
		t.Fatal(err)
	}
	made, err := sandboxes.Get(ctx, Name(session), metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if made.Labels[SessionLabel] != session.String() || made.Labels["aviary.io/agent"] != "an-agent" ||
		made.Spec.PodTemplate.ObjectMeta.Labels[SessionLabel] != session.String() {
		t.Errorf("the sandbox and its pod are labelled %v and %v", made.Labels, made.Spec.PodTemplate.ObjectMeta.Labels)
	}
	pod := made.Spec.PodTemplate.Spec
	if *pod.AutomountServiceAccountToken || *pod.EnableServiceLinks {
		t.Errorf("the pod is %+v", pod)
	}
	// The agent is not root and cannot become root.
	if who := pod.SecurityContext; !*who.RunAsNonRoot || *who.RunAsUser != agentUser || *who.RunAsGroup != agentUser ||
		*who.FSGroup != agentUser || *who.FSGroupChangePolicy != corev1.FSGroupChangeOnRootMismatch ||
		who.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault {
		t.Errorf("the pod runs as %+v", who)
	}
	if may := pod.Containers[0].SecurityContext; *may.AllowPrivilegeEscalation || !slices.Equal(may.Capabilities.Drop, []corev1.Capability{"ALL"}) {
		t.Errorf("the harness may %+v", may)
	}
	harness := pod.Containers[0]
	if harness.Image != opencode.Harness.Image || !slices.Equal(harness.Command, []string{"aviary-session"}) ||
		!harness.Resources.Requests.Cpu().Equal(resource.MustParse("250m")) || !harness.Resources.Limits.Memory().Equal(resource.MustParse("4Gi")) ||
		harness.ReadinessProbe.TCPSocket.Port.IntValue() != RunnerPort ||
		len(harness.VolumeMounts) != 1 || harness.VolumeMounts[0].Name != "home" || harness.VolumeMounts[0].MountPath != "/home" {
		t.Errorf("the harness runs as %+v", harness)
	}
	// What the pod sends out goes through the Egress Proxy, with the token
	// of the session, and the harness has the token in place of a key.
	env := map[string]string{}
	for _, variable := range harness.Env {
		env[variable.Name] = variable.Value
	}
	through := "http://session:the-token-of-the-session@localhost:3128"
	for name, value := range map[string]string{
		"AVIARY_MAX_TOOL_OUTPUT_BYTES": "100",
		"AVIARY_LLM_API_KEY":           "the-token-of-the-session",
		"HTTP_PROXY":                   through, "HTTPS_PROXY": through, "http_proxy": through, "https_proxy": through,
		"NO_PROXY": "localhost,127.0.0.1,::1,localhost", "no_proxy": "localhost,127.0.0.1,::1,localhost",
	} {
		if env[name] != value {
			t.Errorf("the harness is started with %s=%q, not %q", name, env[name], value)
		}
	}
	if len(env) != 8 {
		t.Errorf("the harness is started with %v", env)
	}
	// It has no DNS, and finds the proxy in its hosts file.
	if aliases := pod.HostAliases; len(aliases) != 1 || !slices.Equal(aliases[0].Hostnames, []string{"localhost"}) ||
		!net.ParseIP(aliases[0].IP).IsLoopback() || pod.DNSConfig == nil || len(pod.DNSConfig.Options) != 3 {
		t.Errorf("the pod finds the hosts %v, with %+v", aliases, pod.DNSConfig)
	}
	claims := made.Spec.VolumeClaimTemplates
	if len(claims) != 1 || claims[0].Name != "home" || *claims[0].Spec.StorageClassName != "fast" ||
		!claims[0].Spec.Resources.Requests.Storage().Equal(resource.MustParse("10Gi")) {
		t.Errorf("the volume of the session is %+v", claims)
	}
	if made.Spec.FailedPodPolicy != agents.FailedPodPolicyRecreate || made.Spec.ShutdownTime != nil {
		t.Errorf("the sandbox is %+v", made.Spec)
	}

	// A session has one sandbox.
	if err := c.Create(ctx, request); err != nil {
		t.Errorf("the sandbox of a session that has one was made again: %v", err)
	}
	// One that is kept for a time is deleted then, with its volume.
	kept := uuid.New()
	if err := c.Create(ctx, Request{Session: kept, Pod: opencode, Kept: time.Hour}); err != nil {
		t.Fatal(err)
	}
	made, err = sandboxes.Get(ctx, Name(kept), metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if until := made.Spec.ShutdownTime; until == nil || time.Until(until.Time) < 59*time.Minute || *made.Spec.ShutdownPolicy != agents.ShutdownPolicyDelete {
		t.Errorf("a sandbox that is kept for an hour expires at %v, after which it is %v", until, made.Spec.ShutdownPolicy)
	}
	// Without a class, the volume is of the default class of the cluster.
	c, sandboxes = cluster(t, Settings{Volume: resource.MustParse("1Gi")})
	if err := c.Create(ctx, Request{Session: session, Pod: opencode}); err != nil {
		t.Fatal(err)
	}
	if made, err := sandboxes.Get(ctx, Name(session), metav1.GetOptions{}); err != nil || made.Spec.VolumeClaimTemplates[0].Spec.StorageClassName != nil {
		t.Errorf("the volume of a session is of the class %v: %v", made.Spec.VolumeClaimTemplates[0].Spec.StorageClassName, err)
	}
}

func TestWhatASandboxDoesIsSeenInItsStatus(t *testing.T) {
	// condition is a condition of the status, of the third spec.
	condition := func(kind agents.ConditionType, status metav1.ConditionStatus, reason, message string) metav1.Condition {
		return metav1.Condition{Type: string(kind), Status: status, Reason: reason, Message: message, ObservedGeneration: 3}
	}
	third := metav1.ObjectMeta{Generation: 3}
	now := metav1.Now()
	for _, test := range []struct {
		what    string
		sandbox agents.Sandbox
		state   State
		message string
		address string
	}{
		{what: "a sandbox that is made", state: Starting},
		{
			what: "a sandbox whose pod is not ready",
			sandbox: agents.Sandbox{ObjectMeta: third, Status: agents.SandboxStatus{Conditions: []metav1.Condition{
				condition(agents.SandboxConditionReady, metav1.ConditionFalse, "DependenciesNotReady", "the pod is pending"),
			}}},
			state: Starting, message: "DependenciesNotReady: the pod is pending",
		},
		{
			what: "a sandbox that runs",
			sandbox: agents.Sandbox{ObjectMeta: third, Status: agents.SandboxStatus{PodIPs: []string{"10.0.0.7"}, Conditions: []metav1.Condition{
				condition(agents.SandboxConditionReady, metav1.ConditionTrue, "DependenciesReady", ""),
			}}},
			state: Running, address: "http://10.0.0.7:8700",
		},
		{
			what: "a sandbox that was woken, of which the status still tells of the pod that went",
			sandbox: agents.Sandbox{ObjectMeta: metav1.ObjectMeta{Generation: 4}, Status: agents.SandboxStatus{PodIPs: []string{"10.0.0.7"}, Conditions: []metav1.Condition{
				condition(agents.SandboxConditionReady, metav1.ConditionTrue, "DependenciesReady", ""),
			}}},
			state: Starting,
		},
		{
			what: "a sandbox that is put to sleep, whose pod is still going",
			sandbox: agents.Sandbox{
				ObjectMeta: third,
				Spec:       agents.SandboxSpec{OperatingMode: agents.SandboxOperatingModeSuspended},
				Status: agents.SandboxStatus{PodIPs: []string{"10.0.0.7"}, Conditions: []metav1.Condition{
					condition(agents.SandboxConditionReady, metav1.ConditionTrue, "DependenciesReady", ""),
					condition(agents.SandboxConditionSuspended, metav1.ConditionFalse, "PodTerminating", ""),
				}},
			},
			state: Asleep,
		},
		{
			what: "a sandbox that sleeps",
			sandbox: agents.Sandbox{
				ObjectMeta: third,
				Spec:       agents.SandboxSpec{OperatingMode: agents.SandboxOperatingModeSuspended},
				Status: agents.SandboxStatus{Conditions: []metav1.Condition{
					condition(agents.SandboxConditionSuspended, metav1.ConditionTrue, "PodTerminated", ""),
				}},
			},
			state: Asleep,
		},
		{
			what: "a sandbox that has expired",
			sandbox: agents.Sandbox{ObjectMeta: third, Status: agents.SandboxStatus{Conditions: []metav1.Condition{
				condition(agents.SandboxConditionReady, metav1.ConditionFalse, agents.SandboxReasonExpired, ""),
			}}},
			state: Lost, message: "it has expired",
		},
		{
			what:    "a sandbox that is being deleted",
			sandbox: agents.Sandbox{ObjectMeta: metav1.ObjectMeta{DeletionTimestamp: &now}},
			state:   Lost, message: "it is being deleted",
		},
	} {
		if seen := seen(&test.sandbox); seen.State != test.state || seen.Message != test.message || seen.Address != test.address {
			t.Errorf("%s is seen as %+v", test.what, seen)
		}
	}
}

func TestASandboxSleepsWakesIsKeptAndIsDeleted(t *testing.T) {
	c, sandboxes := cluster(t, Settings{Volume: resource.MustParse("1Gi")})
	session := uuid.New()
	name := Name(session)
	if err := c.Create(ctx, Request{Session: session, Pod: opencode}); err != nil {
		t.Fatal(err)
	}
	spec := func() agents.SandboxSpec {
		t.Helper()
		made, err := sandboxes.Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		return made.Spec
	}
	if err := c.Suspend(ctx, name, ""); err != nil || spec().OperatingMode != agents.SandboxOperatingModeSuspended {
		t.Errorf("the sandbox that is put to sleep is %s: %v", spec().OperatingMode, err)
	}
	// A turn that comes for it now waits for it to wake.
	turn := uuid.New()
	if kept, err := c.Keep(ctx, name, turn, time.Time{}); err != nil || kept.State != Asleep {
		t.Errorf("the sandbox that sleeps is kept as %+v: %v", kept, err)
	}
	// It wakes with the harness as the installation has it now, of which
	// the pod has all and only what it has.
	if err := c.Resume(ctx, name, Pod{Harness: Harness{Image: "harness-opencode:2"}}); err != nil || spec().OperatingMode != agents.SandboxOperatingModeRunning {
		t.Errorf("the sandbox that is woken is %s: %v", spec().OperatingMode, err)
	}
	if harness := spec().PodTemplate.Spec.Containers[0]; harness.Image != "harness-opencode:2" || len(harness.Resources.Requests) != 0 {
		t.Errorf("the sandbox that is woken runs %+v", harness)
	}
	until := time.Now().Add(time.Hour).Truncate(time.Second)
	if kept, err := c.Keep(ctx, name, turn, until); err != nil || kept.State == Asleep || !spec().ShutdownTime.Time.Equal(until) {
		t.Errorf("the sandbox that is kept for an hour is %+v, and expires at %v: %v", kept, spec().ShutdownTime, err)
	}
	if _, err := c.Keep(ctx, name, turn, time.Time{}); err != nil || spec().ShutdownTime != nil {
		t.Errorf("the sandbox that is kept until it is deleted expires at %v: %v", spec().ShutdownTime, err)
	}
	// It is marked with the turn that it was kept for.
	if made, err := sandboxes.Get(ctx, name, metav1.GetOptions{}); err != nil || made.Annotations["aviary.io/turn"] != turn.String() {
		t.Errorf("the sandbox that was kept for the turn %s has the annotations %v: %v", turn, made.Annotations, err)
	}

	if all, err := c.Sandboxes(ctx); err != nil || len(all) != 1 || all[0].Name != name || all[0].Session != session.String() {
		t.Errorf("the sandboxes are %+v: %v", all, err)
	}
	// What is not the sandbox of a session is none of the sandboxes.
	if _, err := sandboxes.Create(ctx, &agents.Sandbox{ObjectMeta: metav1.ObjectMeta{Name: "another"}}, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	if all, err := c.Sandboxes(ctx); err != nil || len(all) != 1 {
		t.Errorf("the sandboxes are %+v: %v", all, err)
	}

	if err := c.Delete(ctx, name); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Sandbox(ctx, name); !errors.Is(err, ErrNotFound) {
		t.Errorf("the sandbox that was deleted: %v", err)
	}
	// One that is gone can be deleted, and not put to sleep, woken or kept.
	if err := c.Delete(ctx, name); err != nil {
		t.Errorf("a sandbox that was deleted could not be deleted: %v", err)
	}
	for what, change := range map[string]func() error{
		"put to sleep": func() error { return c.Suspend(ctx, name, "") },
		"woken":        func() error { return c.Resume(ctx, name, opencode) },
		"kept": func() error {
			_, err := c.Keep(ctx, name, turn, until)
			return err
		},
	} {
		if err := change(); !errors.Is(err, ErrNotFound) {
			t.Errorf("a sandbox that is gone was %s: %v", what, err)
		}
	}
}

func TestASandboxIsPutToSleepOnlyAsItWasSeen(t *testing.T) {
	clients := fake.NewSimpleClientset()
	c, _ := cluster(t, Settings{Volume: resource.MustParse("1Gi")})
	c.sandboxes = clients.AgentsV1beta1().Sandboxes("sessions")
	session := uuid.New()
	if err := c.Create(ctx, Request{Session: session, Pod: opencode}); err != nil {
		t.Fatal(err)
	}
	// The cluster of the test knows no versions, so it answers here as a
	// cluster does to a change of a version that a sandbox no longer has.
	var asked []string
	clients.PrependReactor("patch", "sandboxes", func(action k8stesting.Action) (bool, runtime.Object, error) {
		patch := action.(k8stesting.PatchAction)
		asked = append(asked, string(patch.GetPatch()))
		return true, nil, apierrors.NewConflict(schema.GroupResource{Group: "agents.x-k8s.io", Resource: "sandboxes"}, patch.GetName(), errors.New("the object has been modified"))
	})
	if err := c.Suspend(ctx, Name(session), "7"); !errors.Is(err, ErrChanged) {
		t.Errorf("a sandbox that has changed since it was seen was put to sleep: %v", err)
	}
	if len(asked) != 1 || asked[0] != `{"metadata":{"resourceVersion":"7"},"spec":{"operatingMode":"Suspended"}}` {
		t.Errorf("the cluster was asked %q", asked)
	}
}
