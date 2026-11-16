package driver

import (
	"context"
	"maps"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"

	"aviary/supervisor/internal/store"
)

// config is how a driver is set up in the tests.
func config() Config {
	return Config{
		GatewayURL:  "http://aviary-supervisor.aviary-system:9090",
		RunnerImage: "registry.example/runner:1",
		LLMBaseURL:  "http://aviary-egress-proxy.aviary-system:8080/v1",
		LLMModel:    "a-model",
		ProxyURL:    "http://aviary-egress-proxy.aviary-system:3128",
		Harnesses: map[string]Harness{"opencode": {
			Image:     "registry.example/opencode:1",
			Resources: memory("1Gi"),
		}},
		MaxToolOutputBytes: 65536,
		MaxTurn:            45 * time.Minute,
		MaxReconnect:       90 * time.Second,
	}
}

// memory is the resources of a pod that may use so much memory.
func memory(limit string) corev1.ResourceRequirements {
	return corev1.ResourceRequirements{Limits: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse(limit)}}
}

// A turn takes a few requests to the cluster, and many turns start at once.
func TestClientIsNotHeldBack(t *testing.T) {
	cluster := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"kind": "Job", "apiVersion": "batch/v1", "metadata": {"name": "turn"}}`))
	}))
	defer cluster.Close()
	client, err := newClient(&rest.Config{Host: cluster.URL})
	if err != nil {
		t.Fatal(err)
	}
	began := time.Now()
	for range 60 {
		if _, err := client.BatchV1().Jobs(namespace).Create(context.Background(), &batchv1.Job{}, metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	// A client that is left as it comes takes ten seconds over them.
	if took := time.Since(began); took > 3*time.Second {
		t.Errorf("60 requests to the cluster took %s", took)
	}
}

func TestNew(t *testing.T) {
	if _, err := New(config(), fake.NewClientset(), nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	wrong := config()
	wrong.Harnesses["opencode"] = Harness{Resources: corev1.ResourceRequirements{
		Limits: corev1.ResourceList{"nvidia.com/gpu": resource.MustParse("1")},
	}}
	if _, err := New(wrong, fake.NewClientset(), nil, nil, nil); err == nil {
		t.Error("a harness was taken whose resources are not those of a turn pod")
	}
}

func TestJob(t *testing.T) {
	config := config()
	driver, err := New(config, fake.NewClientset(), nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	dispatch := store.Dispatch{
		TurnID: uuid.New(), SessionID: uuid.New(), AgentID: uuid.New(), ProjectID: uuid.New(),
		Harness: "opencode", Token: "the-token",
	}
	resources := memory("2Gi")
	hosts := []corev1.HostAlias{{IP: "10.96.0.7", Hostnames: []string{"aviary-supervisor.aviary-system"}}}
	job := driver.job(dispatch, config.Harnesses["opencode"], resources, hosts)
	pod := job.Spec.Template.Spec
	check := func(holds bool, otherwise string) {
		t.Helper()
		if !holds {
			t.Error(otherwise)
		}
	}

	turn := dispatch.TurnID.String()
	check(job.Name == "turn-"+turn && job.Labels[turnLabel] == turn && job.Spec.Template.Labels[turnLabel] == turn,
		"the Job and its pod are not found by their turn")
	check(*job.Spec.BackoffLimit == 0 && pod.RestartPolicy == corev1.RestartPolicyNever,
		"a turn that failed is run again, after its agent may have acted")
	check(*job.Spec.ActiveDeadlineSeconds == 45*60, "the pod is not stopped when it has run for as long as a turn may")

	// The pod has no DNS.
	options := map[string]string{}
	for _, option := range pod.DNSConfig.Options {
		options[option.Name] = *option.Value
	}
	check(reflect.DeepEqual(pod.HostAliases, hosts), "the pod is not told the addresses of what it connects to")
	check(options["timeout"] == "1" && options["attempts"] == "1" && options["ndots"] == "1",
		"a program in the pod that looks a name up waits long for its failure")

	check(!*pod.AutomountServiceAccountToken, "the pod holds a token for the Kubernetes API")
	check(!*pod.EnableServiceLinks, "the pod is told of the services in its namespace")
	security := pod.SecurityContext
	check(*security.RunAsNonRoot && *security.RunAsUser == agentUser && *security.RunAsGroup == agentUser &&
		security.SeccompProfile.Type == corev1.SeccompProfileTypeRuntimeDefault,
		"the pod does not run as the user of the harness images, within what a restricted namespace allows")
	for _, container := range slices.Concat(pod.InitContainers, pod.Containers) {
		check(!*container.SecurityContext.AllowPrivilegeEscalation &&
			slices.Equal(container.SecurityContext.Capabilities.Drop, []corev1.Capability{"ALL"}),
			"the container "+container.Name+" is not one that a restricted namespace allows")
		check(reflect.DeepEqual(container.Resources, resources), "the container "+container.Name+" does not get the resources of the turn")
	}

	// The agent sees the home of its session and nothing else of the store.
	// The runner creates the home first, as the user of the agent.
	home := "projects/" + dispatch.ProjectID.String() + "/agents/" + dispatch.AgentID.String() +
		"/sessions/" + dispatch.SessionID.String() + "/home"
	volumes := map[string]corev1.VolumeSource{}
	for _, volume := range pod.Volumes {
		volumes[volume.Name] = volume.VolumeSource
	}
	check(volumes["store"].PersistentVolumeClaim.ClaimName == storeClaim && volumes["runner"].Image.Reference == config.RunnerImage,
		"the pod does not have the store and the runner as its volumes")
	creator := pod.InitContainers[0]
	check(creator.Image == config.RunnerImage &&
		slices.Equal(creator.Command, []string{"/aviary-runner", "init-home", "/store/" + home}) &&
		reflect.DeepEqual(creator.VolumeMounts, []corev1.VolumeMount{{Name: "store", MountPath: "/store"}}),
		"the home of the session is not created before the agent starts")
	agent := pod.Containers[0]
	check(agent.Name == agentContainer && agent.Image == "registry.example/opencode:1" &&
		slices.Equal(agent.Command, []string{"/aviary/bin/aviary-runner"}) && agent.WorkingDir == "/home/workspace",
		"the agent container does not run the runner in the image of the harness")
	check(reflect.DeepEqual(agent.VolumeMounts, []corev1.VolumeMount{
		{Name: "store", MountPath: "/home", SubPath: home},
		{Name: "runner", MountPath: "/aviary/bin", ReadOnly: true},
	}), "the agent container does not mount the home of its session and the runner")

	// The pod holds the token of its turn and no key. What a program in it
	// fetches goes through the proxy, but not what it sends to itself, to
	// the model endpoint or to the Supervisor.
	proxy := "http://turn:the-token@aviary-egress-proxy.aviary-system:3128"
	direct := "localhost,127.0.0.1,::1,aviary-egress-proxy.aviary-system,aviary-supervisor.aviary-system"
	env := map[string]string{}
	for _, variable := range agent.Env {
		env[variable.Name] = variable.Value
	}
	expected := map[string]string{
		"HOME":                         "/home",
		"AVIARY_GATEWAY_URL":           "http://aviary-supervisor.aviary-system:9090",
		"AVIARY_TURN_TOKEN":            "the-token",
		"AVIARY_LLM_BASE_URL":          "http://aviary-egress-proxy.aviary-system:8080/v1",
		"AVIARY_LLM_API_KEY":           "the-token",
		"AVIARY_LLM_MODEL":             "a-model",
		"AVIARY_MAX_TOOL_OUTPUT_BYTES": "65536",
		"AVIARY_MAX_RECONNECT_SECONDS": "90",
		"HTTP_PROXY":                   proxy,
		"HTTPS_PROXY":                  proxy,
		"NO_PROXY":                     direct,
		"http_proxy":                   proxy,
		"https_proxy":                  proxy,
		"no_proxy":                     direct,
	}
	if !maps.Equal(env, expected) {
		t.Errorf("the agent container has the variables\n%v, not\n%v", env, expected)
	}
}

func TestLaunch(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()
	driver, err := New(config(), client, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	// launched returns the resources of the pod that a turn was launched
	// with, or false if none was.
	launched := func(dispatch store.Dispatch) (corev1.ResourceRequirements, bool) {
		t.Helper()
		job, err := client.BatchV1().Jobs(namespace).Get(ctx, jobName(dispatch.TurnID), metav1.GetOptions{})
		if err != nil {
			return corev1.ResourceRequirements{}, false
		}
		return job.Spec.Template.Spec.Containers[0].Resources, true
	}

	// An agent without resources of its own has those of its harness.
	plain := store.Dispatch{TurnID: uuid.New(), Harness: "opencode"}
	if err := driver.launch(ctx, plain, nil); err != nil {
		t.Fatal(err)
	}
	if resources, found := launched(plain); !found || !reflect.DeepEqual(resources, memory("1Gi")) {
		t.Errorf("the pod of an agent without resources of its own was launched %t, with %v", found, resources)
	}
	own := store.Dispatch{TurnID: uuid.New(), Harness: "opencode", Resources: []byte(`{"limits": {"memory": "3Gi"}}`)}
	if err := driver.launch(ctx, own, nil); err != nil {
		t.Fatal(err)
	}
	if resources, found := launched(own); !found || !reflect.DeepEqual(resources, memory("3Gi")) {
		t.Errorf("the pod of an agent with resources of its own was launched %t, with %v", found, resources)
	}

	for name, dispatch := range map[string]store.Dispatch{
		"a harness that is not set up":  {TurnID: uuid.New(), Harness: "another"},
		"resources that cannot be read": {TurnID: uuid.New(), Harness: "opencode", Resources: []byte(`{"limits": "plenty"}`)},
	} {
		if err := driver.launch(ctx, dispatch, nil); err == nil {
			t.Errorf("%s: the turn was launched", name)
		}
		if _, found := launched(dispatch); found {
			t.Errorf("%s: a pod was launched", name)
		}
	}
}

func TestAgentResources(t *testing.T) {
	for _, none := range []string{``, `null`, `{}`, `{"requests": {}}`, `{"requests": {}, "limits": {}}`} {
		resources, err := AgentResources([]byte(none))
		if err != nil || resources != nil {
			t.Errorf("AgentResources(%s) is %s, %v, not nothing", none, resources, err)
		}
	}

	asked := `{"requests": {"cpu": "500m", "memory": "1Gi"}, "limits": {"memory": "2Gi"}}`
	kept := `{"limits":{"memory":"2Gi"},"requests":{"cpu":"500m","memory":"1Gi"}}`
	if resources, err := AgentResources([]byte(asked)); err != nil || string(resources) != kept {
		t.Errorf("AgentResources(%s) is %s, %v, not %s", asked, resources, err, kept)
	}

	for name, refused := range map[string]string{
		"not an object":             `"2Gi"`,
		"an unknown field":          `{"request": {"cpu": "1"}}`,
		"not a quantity":            `{"requests": {"cpu": "plenty"}}`,
		"another resource":          `{"limits": {"nvidia.com/gpu": "1"}}`,
		"a claim":                   `{"claims": [{"name": "gpu"}]}`,
		"below zero":                `{"limits": {"memory": "-1Gi"}}`,
		"a request above its limit": `{"requests": {"memory": "2Gi"}, "limits": {"memory": "1Gi"}}`,
	} {
		if resources, err := AgentResources([]byte(refused)); err == nil {
			t.Errorf("%s: AgentResources(%s) is %s, not an error", name, refused, resources)
		}
	}
}

func TestOutcome(t *testing.T) {
	condition := func(kind batchv1.JobConditionType, status corev1.ConditionStatus) batchv1.JobCondition {
		return batchv1.JobCondition{Type: kind, Status: status}
	}
	tests := []struct {
		name             string
		conditions       []batchv1.JobCondition
		succeeded, ended bool
	}{
		{name: "a job that runs"},
		{
			name:       "a job whose pod succeeded",
			conditions: []batchv1.JobCondition{condition(batchv1.JobComplete, corev1.ConditionTrue)},
			succeeded:  true, ended: true,
		},
		{
			name:       "a job whose pod failed",
			conditions: []batchv1.JobCondition{condition(batchv1.JobFailed, corev1.ConditionTrue)},
			ended:      true,
		},
		{
			// Kubernetes sets these as soon as it knows how the job will end,
			// while the pod may still be stopping.
			name: "a job whose pod has not stopped",
			conditions: []batchv1.JobCondition{
				condition(batchv1.JobFailureTarget, corev1.ConditionTrue),
				condition(batchv1.JobSuccessCriteriaMet, corev1.ConditionTrue),
				condition(batchv1.JobFailed, corev1.ConditionFalse),
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			job := &batchv1.Job{Status: batchv1.JobStatus{Conditions: test.conditions}}
			if succeeded, ended := outcome(job); succeeded != test.succeeded || ended != test.ended {
				t.Errorf("succeeded is %t and ended is %t", succeeded, ended)
			}
		})
	}
}

func TestFailure(t *testing.T) {
	failed := batchv1.JobCondition{Type: batchv1.JobFailed, Status: corev1.ConditionTrue}
	// home is the state of the container that makes the home of the session,
	// which runs before that of the agent.
	home := func(terminated corev1.ContainerStateTerminated) []corev1.ContainerStatus {
		return []corev1.ContainerStatus{{Name: "home", State: corev1.ContainerState{Terminated: &terminated}}}
	}
	agent := func(terminated corev1.ContainerStateTerminated) *corev1.PodStatus {
		return &corev1.PodStatus{
			InitContainerStatuses: home(corev1.ContainerStateTerminated{Reason: "Completed"}),
			ContainerStatuses: []corev1.ContainerStatus{{
				Name: agentContainer, State: corev1.ContainerState{Terminated: &terminated},
			}},
		}
	}
	tests := []struct {
		name      string
		condition batchv1.JobCondition
		// pod is the status of the turn's pod, if the pod is still there.
		pod     *corev1.PodStatus
		failure store.Failure
	}{
		{
			name: "the turn ran for too long",
			condition: batchv1.JobCondition{
				Type: batchv1.JobFailed, Status: corev1.ConditionTrue, Reason: batchv1.JobReasonDeadlineExceeded,
			},
			pod:     agent(corev1.ContainerStateTerminated{ExitCode: 143}),
			failure: store.Failure{Reason: store.FailureTimedOut, Message: "the turn ran for longer than 30m0s"},
		},
		{
			name:      "the pod was deleted",
			condition: failed,
			failure:   store.Failure{Reason: store.FailurePodLost, Message: "the pod of the turn was deleted"},
		},
		{
			name:      "the pod was evicted",
			condition: failed,
			pod:       &corev1.PodStatus{Reason: "Evicted", Message: "The node was low on resource: memory."},
			failure:   store.Failure{Reason: store.FailurePodLost, Message: "The node was low on resource: memory."},
		},
		{
			name:      "the runner left the reason",
			condition: failed,
			pod: agent(corev1.ContainerStateTerminated{
				ExitCode: 1, Message: `{"reason": "harness_error", "message": "the model is out of order"}`,
			}),
			failure: store.Failure{Reason: store.FailureHarness, Message: "the model is out of order"},
		},
		{
			name:      "the runner left a reason that is not its to give",
			condition: failed,
			pod:       agent(corev1.ContainerStateTerminated{ExitCode: 1, Message: `{"reason": "timed_out"}`}),
			failure:   store.Failure{Reason: store.FailureRunner, Message: "the runner ended with exit code 1"},
		},
		{
			name:      "the pod took more memory than it may",
			condition: failed,
			pod:       agent(corev1.ContainerStateTerminated{ExitCode: 137, Reason: "OOMKilled"}),
			failure:   store.Failure{Reason: store.FailureOutOfMemory, Message: "the pod used more memory than it may"},
		},
		{
			name:      "the runner ended when it was told to stop",
			condition: failed,
			pod:       agent(corev1.ContainerStateTerminated{ExitCode: 143}),
			failure:   store.Failure{Reason: store.FailurePodLost, Message: "the pod was stopped"},
		},
		{
			name:      "the runner was killed",
			condition: failed,
			pod:       agent(corev1.ContainerStateTerminated{Signal: 9}),
			failure:   store.Failure{Reason: store.FailurePodLost, Message: "the pod was stopped"},
		},
		{
			name:      "the runner failed and left no reason",
			condition: failed,
			pod:       agent(corev1.ContainerStateTerminated{ExitCode: 101}),
			failure:   store.Failure{Reason: store.FailureRunner, Message: "the runner ended with exit code 101"},
		},
		{
			name:      "the home of the session could not be made",
			condition: failed,
			pod: &corev1.PodStatus{InitContainerStatuses: home(corev1.ContainerStateTerminated{
				ExitCode: 1, Message: `{"reason": "runner_error", "message": "the home of the session could not be made"}`,
			})},
			failure: store.Failure{Reason: store.FailureRunner, Message: "the home of the session could not be made"},
		},
		{
			name:      "the runner failed before the agent started and left no reason",
			condition: failed,
			pod:       &corev1.PodStatus{InitContainerStatuses: home(corev1.ContainerStateTerminated{ExitCode: 101})},
			failure:   store.Failure{Reason: store.FailureRunner, Message: "the runner ended with exit code 101"},
		},
		{
			name:      "no container of the pod ran",
			condition: failed,
			pod:       &corev1.PodStatus{Reason: "OutOfcpu", Message: "Node didn't have enough resource: cpu"},
			failure:   store.Failure{Reason: store.FailureRunner, Message: "the pod failed before the agent started"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			labels := map[string]string{turnLabel: uuid.NewString()}
			// The pod of another turn, which must not be taken for this one.
			objects := []runtime.Object{&corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "another", Namespace: namespace, Labels: map[string]string{turnLabel: uuid.NewString()},
				},
				Status: *agent(corev1.ContainerStateTerminated{ExitCode: 1, Message: `{"reason": "resume_failed"}`}),
			}}
			if test.pod != nil {
				objects = append(objects, &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{Name: "pod", Namespace: namespace, Labels: labels},
					Status:     *test.pod,
				})
			}
			driver := &Driver{config: Config{MaxTurn: 30 * time.Minute}, client: fake.NewClientset(objects...)}
			job := &batchv1.Job{
				ObjectMeta: metav1.ObjectMeta{Name: "job", Namespace: namespace, Labels: labels},
				Status:     batchv1.JobStatus{Conditions: []batchv1.JobCondition{test.condition}},
			}
			if failure := driver.failure(context.Background(), job); failure != test.failure {
				t.Errorf("the failure is %+v, not %+v", failure, test.failure)
			}
		})
	}
}

func TestHoldup(t *testing.T) {
	waiting := func(reason, message string) []corev1.ContainerStatus {
		return []corev1.ContainerStatus{{State: corev1.ContainerState{
			Waiting: &corev1.ContainerStateWaiting{Reason: reason, Message: message},
		}}}
	}
	tests := []struct {
		name   string
		pod    *corev1.Pod
		holdup string
	}{
		{name: "no pod", holdup: "the pod was not created"},
		{
			name: "a pod that fits on no node",
			pod: &corev1.Pod{Status: corev1.PodStatus{Conditions: []corev1.PodCondition{{
				Type: corev1.PodScheduled, Status: corev1.ConditionFalse,
				Reason: "Unschedulable", Message: "0/3 nodes are available: 3 Insufficient cpu.",
			}}}},
			holdup: "Unschedulable: 0/3 nodes are available: 3 Insufficient cpu.",
		},
		{
			name: "an image of the harness that cannot be pulled",
			pod: &corev1.Pod{Status: corev1.PodStatus{
				ContainerStatuses: waiting("ErrImagePull", "registry.invalid/harness: not found"),
			}},
			holdup: "ErrImagePull: registry.invalid/harness: not found",
		},
		{
			name: "an image of the runner that cannot be pulled",
			pod: &corev1.Pod{Status: corev1.PodStatus{
				InitContainerStatuses: waiting("ImagePullBackOff", "back-off pulling the image"),
				ContainerStatuses:     waiting("PodInitializing", ""),
			}},
			holdup: "ImagePullBackOff: back-off pulling the image",
		},
		{
			name: "a pod that Kubernetes has nothing to say about",
			pod: &corev1.Pod{Status: corev1.PodStatus{
				Conditions:        []corev1.PodCondition{{Type: corev1.PodScheduled, Status: corev1.ConditionTrue}},
				ContainerStatuses: waiting("ContainerCreating", ""),
			}},
			holdup: "the runner did not connect",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if holdup := holdup(test.pod); holdup != test.holdup {
				t.Errorf("the holdup is %q, not %q", holdup, test.holdup)
			}
		})
	}
}
