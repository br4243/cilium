// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package check

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"net"
	"slices"
	"time"

	"github.com/blang/semver/v4"
	"github.com/cloudflare/cfssl/cli/genkey"
	"github.com/cloudflare/cfssl/config"
	"github.com/cloudflare/cfssl/csr"
	"github.com/cloudflare/cfssl/helpers"
	"github.com/cloudflare/cfssl/initca"
	"github.com/cloudflare/cfssl/signer"
	"github.com/cloudflare/cfssl/signer/local"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/yaml"

	"github.com/cilium/cilium/cilium-cli/defaults"
	"github.com/cilium/cilium/cilium-cli/k8s"
	"github.com/cilium/cilium/cilium-cli/sysdump"
	"github.com/cilium/cilium/cilium-cli/utils/features"
	k8sConst "github.com/cilium/cilium/pkg/k8s/apis/cilium.io"
	ciliumv2 "github.com/cilium/cilium/pkg/k8s/apis/cilium.io/v2"
	"github.com/cilium/cilium/pkg/lock"
	"github.com/cilium/cilium/pkg/versioncheck"
)

const (
	// KubernetesSourcedLabelPrefix is the optional prefix used in labels to
	// indicate they are sourced from Kubernetes.
	// NOTE: For some reason, ':' gets replaced by '.' in keys so we use that instead.
	KubernetesSourcedLabelPrefix = "k8s."

	// AnySourceLabelPrefix is the optional prefix used in labels to
	// indicate they could be from anywhere.
	// NOTE: For some reason, ':' gets replaced by '.' in keys so we use that instead.
	AnySourceLabelPrefix = "any."
)

var (
	//go:embed assets/cacert.pem
	caBundle []byte

	//go:embed manifests/egress-gateway-policy.yaml
	egressGatewayPolicyYAML string
)

// NewTest factory function.
func NewTest(name string, verbose bool, debug bool) *Test {
	if name == "" {
		panic("empty test name")
	}
	test := &Test{
		name:        name,
		scenarios:   make(map[Scenario][]*Action),
		resources:   []k8s.Object{},
		clrps:       make(map[string]*ciliumv2.CiliumLocalRedirectPolicy),
		logBuf:      &bytes.Buffer{}, // maintain internal buffer by default
		conditionFn: nil,
		verbose:     verbose,
	}
	// Setting the internal buffer to nil causes the logger to
	// write directly to stdout in verbose or debug mode.
	if verbose || debug {
		test.logBuf = nil
	}
	return test
}

type Test struct {
	// Reference to the enclosing test suite for logging etc.
	ctx *ConnectivityTest

	// Name of the test. Must be unique within the scope of a test run.
	name string

	// True if the Test is marked as skipped.
	skipped bool

	// True if the Test is marked as failed.
	failed bool

	// requirements is a list of required Cilium features which need to match
	// for this test to be run
	requirements []features.Requirement

	// installIPRoutesFromOutsideToPodCIDRs indicates that the test runner needs
	// to install podCIDR => nodeIP routes before running the test
	installIPRoutesFromOutsideToPodCIDRs bool

	// versionRange is the version range the Cilium agent needs to be in in order
	// for the test to run.
	versionRange string

	// Scenarios registered to this test.
	scenarios map[Scenario][]*Action

	// Scenarios marked as skipped during execution.
	// Needs to be stored as a list, these are implemented in another package.
	scenariosSkipped []Scenario

	// Cilium Local Redirect Policies active during this test.
	clrps map[string]*ciliumv2.CiliumLocalRedirectPolicy

	// k8s resources that should be created before the test run, and removed afterwards.
	// If any of these correspond to a network policy, this will wait for the policy revision
	// to be incremented.
	resources []k8s.Object

	// Secrets that have to be present during the test.
	secrets map[string]*corev1.Secret

	// CA certificates of the certificates that have to be present during the test.
	certificateCAs  map[string][]byte
	certificateKeys map[string][]byte

	// A custom sysdump policy for the given test.
	sysdumpPolicy SysdumpPolicy

	// List of callbacks to be executed before the test run as additional setup.
	before []SetupFunc

	expectFunc ExpectationsFunc

	// Start time of the test.
	startTime time.Time

	// Completion time of the test.
	completionTime time.Time

	// Buffer to store output until it's flushed by a failure.
	// Unused when run in verbose or debug mode.
	logMu   lock.RWMutex
	logBuf  io.ReadWriter
	verbose bool

	// conditionFn is a function that returns true if the test needs to run,
	// and false otherwise. By default, it's set to a function that returns
	// true.
	conditionFn []func() bool

	// List of functions to be called when Run() returns.
	finalizers []func(ctx context.Context) error
}

func (t *Test) String() string {
	return fmt.Sprintf("<Test %s, %d scenarios, %d resources, expectFunc %v>",
		t.name, len(t.scenarios), len(t.resources), t.expectFunc)
}

// Name returns the name of the test.
func (t *Test) Name() string {
	return t.name
}

func (t *Test) Failed() bool {
	return t.failed
}

func (t *Test) FailureMessages() []string {
	failureMessages := []string{}
	for _, s := range t.scenarios {
		for _, m := range s {
			if m.failureMessage != "" {
				failureMessages = append(failureMessages, m.failureMessage)
			}
		}
	}
	return failureMessages
}

// ScenarioName returns the Test name and Scenario name concatenated in
// a standard way. Scenario names are not unique, as they can occur multiple
// times in the same Test.
func (t *Test) scenarioName(s Scenario) string {
	return fmt.Sprintf("%s/%s", t.Name(), s.Name())
}

// scenarioEnabled returns true if the given scenario is enabled by the user.
func (t *Test) scenarioEnabled(s Scenario) bool {
	return t.Context().params.testEnabled(t.scenarioName(s))
}

// scenarioRequirements returns true if the Cilium deployment meets the
// requirements of the given Scenario.
func (t *Test) scenarioRequirements(s Scenario) (bool, string) {
	var reqs []features.Requirement
	if cs, ok := s.(ConditionalScenario); ok {
		reqs = cs.Requirements()
	}

	return t.Context().Features.MatchRequirements(reqs...)
}

// Context returns the enclosing context of the Test.
func (t *Test) Context() *ConnectivityTest {
	return t.ctx
}

// setup sets up the environment for the Test to execute in, like applying secrets and CNPs.
func (t *Test) setup(ctx context.Context) error {

	// Apply Secrets to the cluster.
	if err := t.applySecrets(ctx); err != nil {
		t.ContainerLogs(ctx)
		return fmt.Errorf("applying Secrets: %w", err)
	}

	// Apply CNPs & KNPs to the cluster.
	if err := t.applyResources(ctx); err != nil {
		t.ContainerLogs(ctx)
		return fmt.Errorf("applying network policies: %w", err)
	}

	if t.installIPRoutesFromOutsideToPodCIDRs {
		// Attempt to cleanup any leftover routes in case tests previously
		// didn't cleanup correctly.
		t.Context().modifyStaticRoutesForNodesWithoutCilium(ctx, "del")
		if err := t.Context().modifyStaticRoutesForNodesWithoutCilium(ctx, "add"); err != nil {
			return fmt.Errorf("installing static routes: %w", err)
		}

		t.finalizers = append(t.finalizers, func(context.Context) error {
			return t.Context().modifyStaticRoutesForNodesWithoutCilium(ctx, "del")
		})
	}

	return nil
}

// skip adds Scenario s to the Test's list of skipped Scenarios.
// This list is kept for reporting purposes.
func (t *Test) skip(s Scenario, reason string) {
	t.scenariosSkipped = append(t.scenariosSkipped, s)
	t.Logf("[-] Skipping Scenario [%s] (%s)", t.scenarioName(s), reason)
}

// versionInRange returns true if the given (running) version is within the
// range allowed by the test. The second return value is a string mentioning the
// given version and the expected range.
func (t *Test) versionInRange(version semver.Version) (bool, string) {
	if t.versionRange == "" {
		return true, "no version requirement"
	}

	vr := versioncheck.MustCompile(t.versionRange)
	if !vr(version) {
		return false, fmt.Sprintf("requires Cilium version %v but running %s", t.versionRange, version)
	}

	return true, "running version within range"
}

func (t *Test) checkConditions() bool {
	for _, fn := range t.conditionFn {
		if !fn() {
			return false
		}
	}
	return true
}

// willRun returns false if all of the Test's Scenarios are skipped by the user,
// if any of its FeatureRequirements are not met, or if the running Cilium
// version is not within range of the one specified using WithCiliumVersion.
//
// Currently, only a single reason is communicated for skipping a test.
// Following the principle of least surprise, the checks in this method are
// ordered to return the least-obvious reason first. If the user is explicitly
// excluding tests, they're most likely interested in other reasons why their
// test is not being executed.
func (t *Test) willRun() (bool, string) {
	if !t.checkConditions() {
		return false, "skipped by condition"
	}

	// Check if the running Cilium version is within range of the value specified
	// in WithCiliumVersion.
	if ver, reason := t.versionInRange(t.Context().CiliumVersion); !ver {
		return false, reason
	}

	// Check the Test's specified feature requirements.
	if req, reason := t.Context().Features.MatchRequirements(t.requirements...); !req {
		return false, reason
	}

	// Skip the whole Test if all of its Scenarios are excluded by the user's
	// filter.
	var skipped int
	for s := range t.scenarios {
		if !t.Context().params.testEnabled(t.scenarioName(s)) {
			skipped++
		}
	}
	if skipped == len(t.scenarios) {
		return false, "skipped by user"
	}

	return true, ""
}

// finalize runs all the Test's registered finalizers.
// Failures encountered executing finalizers will fail the Test.
func (t *Test) finalize() {
	t.Debug("Finalizing Test", t.Name())

	// Iterate finalizers in backward order.
	// As an example, first we create secrets that are referenced in policies.
	// When performing cleanup, we want to first delete policies and then secrets.
	for _, f := range slices.Backward(t.finalizers) {
		// Use a detached context to make sure this call is not affected by
		// context cancellation. Usually, finalization (e.g., netpol removal)
		// needs to happen even when the user interrupted the program.
		if err := f(context.TODO()); err != nil {
			t.Failf("Error finalizing '%s': %s", t.Name(), err)
		}
	}
}

// Run executes all Scenarios registered to the Test.
func (t *Test) Run(ctx context.Context, index int) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	// Steps to execute when all Scenarios have finished executing,
	// whether they were successful or not. Scenario.Run() might call Fatal(),
	// in which case this function executes as normal.
	defer func() {
		// Run all the Test's registered finalizers.
		t.finalize()
	}()

	if len(t.scenarios) == 0 {
		t.Failf("Test has no Scenarios [%d/%d]", index, len(t.ctx.tests))
	}

	// Skip the Test if all of its Scenarios are skipped.
	if run, reason := t.willRun(); !run {
		t.Context().skip(t, index, reason)
		return nil
	}

	// Store start time of the Test.
	t.startTime = time.Now()
	// Store completion of the Test when function is returned
	defer func() {
		t.completionTime = time.Now()
	}()

	t.ctx.logger.Printf(t, "[=] [%s] Test [%s] [%d/%d]\n", t.ctx.params.TestNamespace, t.Name(), index, len(t.ctx.tests))

	if err := t.setup(ctx); err != nil {
		return fmt.Errorf("setting up test: %w", err)
	}

	for _, cb := range t.before {
		if err := cb(ctx, t, t.ctx); err != nil {
			return fmt.Errorf("additional test setup callback: %w", err)
		}
	}

	if t.logBuf != nil {
		t.ctx.Timestamp()
	}

	for s := range t.scenarios {
		if err := ctx.Err(); err != nil {
			return err
		}

		if !t.scenarioEnabled(s) {
			t.skip(s, "skipped by user")
			continue
		}

		if req, reason := t.scenarioRequirements(s); !req {
			t.skip(s, reason)
			continue
		}

		t.Logf("[-] Scenario [%s]", t.scenarioName(s))

		s.Run(ctx, t)
	}

	if t.logBuf != nil {
		t.ctx.logger.Printf(t, "\n")
	}

	// Don't add any more code here, as Scenario.Run() can call Fatal() and
	// terminate this goroutine.

	return nil
}

// WithCondition takes a function containing condition check logic that
// returns true if the test needs to be run, and false otherwise. If
// WithCondition gets called multiple times, all the conditions need to be
// satisfied for the test to run.
func (t *Test) WithCondition(fn func() bool) *Test {
	t.conditionFn = append(t.conditionFn, fn)
	return t
}

// WithResources registers the list of one or more YAML-defined
// Kubernetes resources (e.g. NetworkPolicy, etc.)
//
// # For certain well-known types, known references to the namespace are mutated
//
// If the resource has a namepace of "cilium-test", that is mutated
// to the (serialized) namespace of the individual scenario.
func (t *Test) WithResources(spec string) *Test {
	buf := bytes.Buffer{}
	buf.WriteString(spec)
	decoder := yaml.NewYAMLOrJSONDecoder(&buf, 4096)

	for {
		u := unstructured.Unstructured{}
		if err := decoder.Decode(&u); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatalf("Parsing resource YAML: %s", err)
		}

		if u.GetNamespace() == defaults.ConnectivityCheckNamespace {
			u.SetNamespace(t.ctx.params.TestNamespace)
		}
		t.resources = append(t.resources, t.tweakPolicy(&u))
	}
	return t
}

// WithCiliumPolicy takes a string containing a YAML policy document and adds
// the polic(y)(ies) to the scope of the Test, to be applied when the test
// starts running. When calling this method, note that the CNP enabled feature
// // requirement is applied directly here.
func (t *Test) WithCiliumPolicy(policy string) *Test {
	return t.WithResources(policy)
}

// WithCiliumClusterwidePolicy takes a string containing a YAML policy document
// and adds the clusterwide polic(y)(ies) to the scope of the Test, to be applied
// when the test starts running. When calling this method, note that the CCNP
// enabled feature requirement is applied directly here.
func (t *Test) WithCiliumClusterwidePolicy(policy string) *Test {
	return t.WithResources(policy)
}

// WithK8SPolicy takes a string containing a YAML policy document and adds
// the polic(y)(ies) to the scope of the Test, to be applied when the test
// starts running. When calling this method, note that the KNP enabled feature
// requirement is applied directly here.
func (t *Test) WithK8SPolicy(policy string) *Test {
	return t.WithResources(policy)
}

// CiliumLocalRedirectPolicyParams is used to configure a CiliumLocalRedirectPolicy template.
type CiliumLocalRedirectPolicyParams struct {
	// Policy is the local redirect policy yaml.
	Policy string

	// Name is the name of the local redirect policy.
	Name string

	// FrontendIP is the IP address of the address matcher frontend set in the policy spec.
	FrontendIP string

	// SkipRedirectFromBackend is the flag set in the policy spec.
	SkipRedirectFromBackend bool
}

func (t *Test) WithCiliumLocalRedirectPolicy(params CiliumLocalRedirectPolicyParams) *Test {
	pl := ciliumv2.CiliumLocalRedirectPolicy{}
	if err := parseInto([]byte(params.Policy), &pl); err != nil {
		t.Fatalf("Parsing local redirect policy YAML: %s", err)
	}

	pl.Namespace = t.ctx.params.TestNamespace
	pl.Name = params.Name
	pl.Spec.RedirectFrontend.AddressMatcher.IP = params.FrontendIP
	pl.Spec.SkipRedirectFromBackend = params.SkipRedirectFromBackend

	t.resources = append(t.resources, &pl)
	t.clrps[params.Name] = &pl

	t.WithFeatureRequirements(features.RequireEnabled(features.LocalRedirectPolicy))

	return t
}

type ExcludedCIDRsKind int

const (
	// NoExcludedCIDRs does not configure any excluded CIDRs in the policy
	NoExcludedCIDRs ExcludedCIDRsKind = iota

	// ExternalNodeExcludedCIDRs adds the IPs of the external nodes (i.e the ones with the "cilium.io/no-schedule" label) to the list of excluded CIDRs
	ExternalNodeExcludedCIDRs
)

// CiliumEgressGatewayPolicyParams is used to configure how a CiliumEgressGatewayPolicy template should be configured
// before being applied.
type CiliumEgressGatewayPolicyParams struct {
	// Name controls the name of the policy
	Name string

	// PodSelectorKind is used to select the client pods. The parameter is used to select pods with a matching "kind" label
	PodSelectorKind string

	// ExcludedCIDRsConf controls how the ExcludedCIDRsConf property should be configured
	ExcludedCIDRsConf ExcludedCIDRsKind

	// Includes changes for multigateway testing
	Multigateway bool
}

// WithCiliumEgressGatewayPolicy takes a string containing a YAML policy
// document and adds the cilium egress gateway polic(y)(ies) to the scope of the
// Test, to be applied when the test starts running. When calling this method,
// note that the egress gateway enabled feature requirement is applied directly
// here.
func (t *Test) WithCiliumEgressGatewayPolicy(params CiliumEgressGatewayPolicyParams) *Test {
	pl := ciliumv2.CiliumEgressGatewayPolicy{}
	if err := parseInto([]byte(egressGatewayPolicyYAML), &pl); err != nil {
		t.Fatalf("Parsing EgressGatewayPolicy: %s", err)
	}

	// Change the default test namespace as required.
	for _, k := range []string{
		k8sConst.PodNamespaceLabel,
		KubernetesSourcedLabelPrefix + k8sConst.PodNamespaceLabel,
		AnySourceLabelPrefix + k8sConst.PodNamespaceLabel,
	} {
		for _, e := range pl.Spec.Selectors {
			ps := e.PodSelector
			if n, ok := ps.MatchLabels[k]; ok && n == defaults.ConnectivityCheckNamespace {
				ps.MatchLabels[k] = t.ctx.params.TestNamespace
			}
		}
	}

	// Set the policy name
	pl.Name = params.Name

	// Set the pod selector
	pl.Spec.Selectors[0].PodSelector.MatchLabels["kind"] = params.PodSelectorKind

	// Set the egress gateway node
	egressGatewayNode := t.EgressGatewayNode()
	if egressGatewayNode == "" {
		t.Fatalf("Cannot find egress gateway node")
	}

	pl.Spec.EgressGateway.NodeSelector.MatchLabels["kubernetes.io/hostname"] = egressGatewayNode

	// If the field EgressGateways is set, the contents of the field EgressGateway are disregarded.
	if params.Multigateway && versioncheck.MustCompile(">=1.18.0")(t.ctx.CiliumVersion) {
		egressGatewayNodes := t.EgressGatewayNodes()
		for _, node := range egressGatewayNodes {
			gw := pl.Spec.EgressGateway.DeepCopy()
			gw.NodeSelector.MatchLabels["kubernetes.io/hostname"] = node
			pl.Spec.EgressGateways = append(pl.Spec.EgressGateways, *gw)
		}
	}

	var ipv6Enabled bool
	if status, ok := t.ctx.Feature(features.IPv6); ok && status.Enabled && versioncheck.MustCompile(">=1.18.0")(t.ctx.CiliumVersion) {
		ipv6Enabled = true
	}

	// If IPv6 egress policies are enabled, add the necessary destination CIDR
	if ipv6Enabled {
		pl.Spec.DestinationCIDRs = append(pl.Spec.DestinationCIDRs, "::/0")
	}

	// Set the excluded CIDRs
	pl.Spec.ExcludedCIDRs = []ciliumv2.CIDR{}

	switch params.ExcludedCIDRsConf {
	case ExternalNodeExcludedCIDRs:
		for _, nodeWithoutCiliumIP := range t.Context().params.NodesWithoutCiliumIPs {
			if parsedIP := net.ParseIP(nodeWithoutCiliumIP.IP); parsedIP.To4() == nil {
				// If it is an IPv6 address, add the necessary excluded CIDR
				if ipv6Enabled {
					cidr := ciliumv2.CIDR(fmt.Sprintf("%s/128", nodeWithoutCiliumIP.IP))
					pl.Spec.ExcludedCIDRs = append(pl.Spec.ExcludedCIDRs, cidr)
				}
				continue
			}

			cidr := ciliumv2.CIDR(fmt.Sprintf("%s/32", nodeWithoutCiliumIP.IP))
			pl.Spec.ExcludedCIDRs = append(pl.Spec.ExcludedCIDRs, cidr)
		}
	}

	t.resources = append(t.resources, &pl)

	t.WithFeatureRequirements(features.RequireEnabled(features.EgressGateway))
	if params.Multigateway {
		t.WithCiliumVersion(">=1.18.0")
	}

	return t
}

// WithScenarios adds Scenarios to Test in the given order.
func (t *Test) WithScenarios(sl ...Scenario) *Test {
	// Disallow adding the same Scenario object multiple times.
	for _, s := range sl {
		if _, ok := t.scenarios[s]; ok {
			t.Fatalf("Scenario %v already in %s's list of Scenarios", s, t)
		}

		t.scenarios[s] = make([]*Action, 0)
	}

	return t
}

// WithFeatureRequirements adds FeatureRequirements to Test, all of which
// must be satisfied in order for the test to be run. It adds only features
// that are not already present in the requirements.
func (t *Test) WithFeatureRequirements(reqs ...features.Requirement) *Test {
	if len(reqs) == 0 {
		return t
	}

	for _, target := range reqs {
		var seen bool
		for _, r := range t.requirements {
			if target == r {
				// Save the state of the target as seen if already in the requirements list.
				seen = true
			}
		}
		if !seen {
			// Target requirement not present, let's add it.
			t.requirements = append(t.requirements, target)
		}
	}

	return t
}

// WithIPRoutesFromOutsideToPodCIDRs instructs the test runner that
// podCIDR => nodeIP routes needs to be installed on a node which doesn't run
// Cilium before running the test (and removed after the test completion).
func (t *Test) WithIPRoutesFromOutsideToPodCIDRs() *Test {
	t.installIPRoutesFromOutsideToPodCIDRs = true
	return t
}

// WithCiliumVersion limits test execution to Cilium versions that fall within
// the given range. The input string is passed to [semver.ParseRange], see
// package semver. Simple examples: ">1.0.0 <2.0.0" or ">=1.14.0".
func (t *Test) WithCiliumVersion(vr string) *Test {
	// Compile the input but don't store the result. A semver.Range is a func()
	// that doesn't implement String(), so the original version constraint cannot
	// be recovered to display to the user. The original constraint is echoed in
	// Test/Scenario skip messages together with the running Cilium version.
	_ = versioncheck.MustCompile(vr)
	t.versionRange = vr
	return t
}

// WithSecret takes a Secret and adds it to the cluster during the test
func (t *Test) WithSecret(secret *corev1.Secret) *Test {

	// Change namespace of the secret to the test namespace
	secret.SetNamespace(t.ctx.params.TestNamespace)

	if err := t.addSecrets(secret); err != nil {
		t.Fatalf("Adding secret: %s", err)
	}
	return t
}

// WithCABundleSecret makes the secret `cabundle` with a CA bundle and adds it to the cluster
func (t *Test) WithCABundleSecret() *Test {
	if len(caBundle) == 0 {
		t.Fatalf("CA bundle is empty")
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cabundle",
			Namespace: t.ctx.params.TestNamespace,
		},
		Data: map[string][]byte{
			"ca.crt": caBundle,
		},
	}

	if err := t.addSecrets(secret); err != nil {
		t.Fatalf("Adding CA bundle secret: %s", err)
	}
	return t
}

// WithCertificate makes a secret with a certificate and adds it to the cluster
func (t *Test) WithCertificate(name, hostname string) *Test {
	caCert, _, caKey, err := initca.New(&csr.CertificateRequest{
		KeyRequest: csr.NewKeyRequest(),
		CN:         "Cilium Test CA",
	})
	if err != nil {
		t.Fatalf("Unable to create CA: %s", err)
	}

	g := &csr.Generator{Validator: genkey.Validator}
	csrBytes, keyBytes, err := g.ProcessRequest(&csr.CertificateRequest{
		CN:    hostname,
		Hosts: []string{hostname},
	})
	if err != nil {
		t.Fatalf("Unable to create CSR: %s", err)
	}
	parsedCa, err := helpers.ParseCertificatePEM(caCert)
	if err != nil {
		t.Fatalf("Unable to parse CA: %s", err)
	}
	caPriv, err := helpers.ParsePrivateKeyPEM(caKey)
	if err != nil {
		t.Fatalf("Unable to parse CA key: %s", err)
	}

	signConf := &config.Signing{
		Default: &config.SigningProfile{
			Expiry: 365 * 24 * time.Hour,
			Usage:  []string{"key encipherment", "server auth", "digital signature"},
		},
	}

	s, err := local.NewSigner(caPriv, parsedCa, signer.DefaultSigAlgo(caPriv), signConf)
	if err != nil {
		t.Fatalf("Unable to create signer: %s", err)
	}
	certBytes, err := s.Sign(signer.SignRequest{Request: string(csrBytes)})
	if err != nil {
		t.Fatalf("Unable to sign certificate: %s", err)
	}

	if t.certificateCAs == nil {
		t.certificateCAs = make(map[string][]byte)
	}
	t.certificateCAs[name] = caCert

	if t.certificateKeys == nil {
		t.certificateKeys = make(map[string][]byte)
	}
	t.certificateKeys[name] = caKey

	return t.WithSecret(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Type: corev1.SecretTypeTLS,
		Data: map[string][]byte{
			corev1.TLSCertKey:       certBytes,
			corev1.TLSPrivateKeyKey: keyBytes,
		},
	})
}

// SetupFunc is a callback meant to be called before running the test.
// It performs additional setup needed to run tests.
type SetupFunc func(ctx context.Context, t *Test, testCtx *ConnectivityTest) error

// WithSetupFunc registers a SetupFunc callback to be executed just before
// the test runs.
func (t *Test) WithSetupFunc(f SetupFunc) *Test {
	t.before = append(t.before, f)
	return t
}

// WithFinalizer registers a finalizer to be executed when Run() returns.
func (t *Test) WithFinalizer(f func(context.Context) error) *Test {
	t.finalizers = append(t.finalizers, f)
	return t
}

// SysdumpPolicy represents a policy for sysdump collection in case of test failure.
type SysdumpPolicy int

const (
	// SysdumpPolicyEach enables capturing one sysdump for each failing action.
	// This is the default and applies also when no explicit policy is specified.
	SysdumpPolicyEach SysdumpPolicy = iota
	// SysdumpPolicyOnce enables capturing only one sysdump for the given test,
	// independently of the number of failures.
	SysdumpPolicyOnce
	// SysdumpPolicyNever disables sysdump collection for the given test.
	SysdumpPolicyNever
)

// WithSysdumpPolicy enables tuning the policy for capturing the sysdump in case
// of test failure, which takes effect only when sysdumps have been requested by
// the user. It is intended to be used to limit the number of sysdumps generated
// in case of multiple subsequent failures, if they would not contain additional
// information (e.g., when asserting the absence of log errors over multiple pods).
func (t *Test) WithSysdumpPolicy(policy SysdumpPolicy) *Test {
	t.sysdumpPolicy = policy
	return t
}

// NewAction creates a new Action. s must be the Scenario the Action is created
// for, name should be a visually-distinguishable name, src is the execution
// Pod of the action, and dst is the network target the Action will connect to.
func (t *Test) NewAction(s Scenario, name string, src *Pod, dst TestPeer, ipFam features.IPFamily) *Action {
	a := newAction(t, name, s, src, dst, ipFam)

	// Obtain the expected result for this particular action by calling
	// the registered expectation function.
	a.expEgress, a.expIngress = t.expectations(a)

	// Store a list of Actions per Scenario.
	t.scenarios[s] = append(t.scenarios[s], a)

	return a
}

// NewGenericAction creates a new Action not associated with any execution pod
// nor network target, but intended for generic assertions (e.g., checking the
// absence of log errors over multiple pods). s must be the Scenario the Action
// is created for, name should be a visually-distinguishable name.
func (t *Test) NewGenericAction(s Scenario, name string) *Action {
	return t.NewAction(s, name, nil, nil, features.IPFamilyAny)
}

// Scenarios returns a slice of all Scenarios belonging to the Test.
func (t *Test) Scenarios() []Scenario {
	var out []Scenario

	for s := range t.scenarios {
		out = append(out, s)
	}

	return out
}

// failedActions returns a list of failed Actions in the Test.
func (t *Test) failedActions() []*Action {
	var out []*Action

	for _, s := range t.scenarios {
		for _, a := range s {
			if a.failed {
				out = append(out, a)
			}
		}
	}

	return out
}

func (t *Test) NodesWithoutCilium() []string {
	return t.ctx.NodesWithoutCilium()
}

// EgressGatewayNode returns the name of the node that is supposed to act as
// egress gateway in the egress gateway tests.
//
// Currently the designated node is the one running the other=client client pod.
func (t *Test) EgressGatewayNode() string {
	for _, clientPod := range t.ctx.clientPods {
		if clientPod.Pod.Labels["other"] == "client" {
			return clientPod.Pod.Spec.NodeName
		}
	}

	return ""
}

// Like EgressGatewayNode() but for the multigateway test case.
// In this case we use the pods with labels other=client and other=client-other-node.
func (t *Test) EgressGatewayNodes() []string {
	var out []string
	for _, clientPod := range t.ctx.clientPods {
		if clientPod.Pod.Labels["other"] == "client" ||
			clientPod.Pod.Labels["other"] == "client-other-node" {
			out = append(out, clientPod.Pod.Spec.NodeName)
		}
	}
	return out
}

func (t *Test) collectSysdump() {
	for _, client := range t.ctx.Clients() {
		collector, err := sysdump.NewCollector(client, t.ctx.params.SysdumpOptions, t.ctx.sysdumpHooks, time.Now())
		if err != nil {
			t.Failf("Failed to create sysdump collector: %v", err)
			return
		}
		if err = collector.Run(); err != nil {
			t.Failf("Failed to collect sysdump: %v", err)
		}
	}
}

func (t *Test) ForEachIPFamily(do func(features.IPFamily)) {
	t.ctx.ForEachIPFamily(t.HasNetworkPolicies(), do)
}

// CertificateCAs returns the CAs used to sign the certificates within the test.
func (t *Test) CertificateCAs() map[string][]byte {
	return t.certificateCAs
}

// CertificateKeys returns the CA keys used to sign the certificates within the test.
func (t *Test) CertificateKeys() map[string][]byte {
	return t.certificateKeys
}

func (t *Test) CiliumLocalRedirectPolicies() map[string]*ciliumv2.CiliumLocalRedirectPolicy {
	return t.clrps
}

func (t *Test) HasNetworkPolicies() bool {
	return slices.ContainsFunc(t.resources, isPolicy)
}
