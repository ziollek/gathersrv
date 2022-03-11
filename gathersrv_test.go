package gathersrv

import (
	"context"
	"github.com/coredns/coredns/plugin/pkg/dnstest"
	"github.com/coredns/coredns/plugin/test"
	"github.com/miekg/dns"
	"github.com/stretchr/testify/require"
	"testing"
)

type Assertion struct {
	GivenName     string
	GivenType     uint16
	ExpectedRcode uint16
	ExpectedError error
}

func TestShouldNotProxyUnqualifiedRequests(t *testing.T) {
	unqualifiedQuestions := map[string]Assertion{
		// asks about type which is not supported by plugin
		"_http._tcp.demo.svc.distro.local.": {
			GivenName:     "_http._tcp.demo.svc.distro.local.",
			GivenType:     dns.TypeMX,
			ExpectedRcode: dns.RcodeNameError,
			ExpectedError: nil,
		},
		// asks about domain which is not handled
		"_http._tcp.demo.svc.unsupported.local.": {
			GivenName:     "_http._tcp.demo.svc.distro.local.",
			GivenType:     dns.TypeSRV,
			ExpectedRcode: dns.RcodeNameError,
			ExpectedError: nil,
		},
	}

	gatherPlugin := &GatherSrv{
		Next:   PrepareOnlyCodeNextHandler(unqualifiedQuestions),
		Domain: "distro.local.",
	}

	for _, assertion := range unqualifiedQuestions {
		CheckAssertion(t, gatherPlugin, assertion)
	}
}

func TestShouldProxyQualifiedRequestsToEachConfiguredCluster(t *testing.T) {
	qualifiedQuestions := map[string]Assertion{
		"srv": {
			GivenName:     "_http._tcp.demo.svc.distro.local.",
			GivenType:     dns.TypeSRV,
			ExpectedRcode: dns.RcodeSuccess,
			ExpectedError: nil,
		},
		"txt": {
			GivenName:     "demo.svc.distro.local.",
			GivenType:     dns.TypeTXT,
			ExpectedRcode: dns.RcodeSuccess,
			ExpectedError: nil,
		},
		"a": {
			GivenName:     "a-demo-0.svc.distro.local.",
			GivenType:     dns.TypeA,
			ExpectedRcode: dns.RcodeSuccess,
			ExpectedError: nil,
		},
		"aaaa": {
			GivenName:     "b-demo-0.svc.distro.local.",
			GivenType:     dns.TypeAAAA,
			ExpectedRcode: dns.RcodeSuccess,
			ExpectedError: nil,
		},
	}

	expectedProxiedQuestions := map[string]Assertion{
		// ask about srv
		"_http._tcp.demo.svc.cluster-a.local.": qualifiedQuestions["srv"],
		"_http._tcp.demo.svc.cluster-b.local.": qualifiedQuestions["srv"],
		// ask about TXT
		"demo.svc.cluster-a.local.": qualifiedQuestions["txt"],
		"demo.svc.cluster-b.local.": qualifiedQuestions["txt"],
		// ask about A with hostname returned by srv
		"demo-0.svc.cluster-a.local.": qualifiedQuestions["a"],
		// ask about AAAA with hostname returned by srv
		"demo-0.svc.cluster-b.local.": qualifiedQuestions["aaaa"],
	}

	gatherPlugin := &GatherSrv{
		Next:   PrepareOnlyCodeNextHandler(expectedProxiedQuestions),
		Domain: "distro.local.",
		Clusters: []Cluster{
			{
				Suffix: "cluster-a.local.",
				Prefix: "a-",
			},
			{
				Suffix: "cluster-b.local.",
				Prefix: "b-",
			},
		},
	}

	for _, assertion := range qualifiedQuestions {
		CheckAssertion(t, gatherPlugin, assertion)
	}
}

func TestShouldTranslateResponsesFromClusters(t *testing.T) {
	assertion := Assertion{
		GivenName:     "_http._tcp.demo.svc.distro.local.",
		GivenType:     dns.TypeSRV,
		ExpectedRcode: dns.RcodeSuccess,
		ExpectedError: nil,
	}
	expectedAnswers := []dns.RR{
		test.SRV("_http._tcp.demo.svc.distro.local. 30 IN SRV 0 50 8080 a-demo-0.svc.distro.local."),
		test.SRV("_http._tcp.demo.svc.distro.local. 30 IN SRV 0 50 8080 a-demo-1.svc.distro.local."),
		test.SRV("_http._tcp.demo.svc.distro.local. 30 IN SRV 0 50 8080 b-demo-0.svc.distro.local."),
		test.SRV("_http._tcp.demo.svc.distro.local. 30 IN SRV 0 50 8080 b-demo-1.svc.distro.local."),
	}

	expectedExtras := []dns.RR{
		test.A("a-demo-0.svc.distro.local. 30 IN A 10.8.1.2"),
		test.A("a-demo-1.svc.distro.local. 30 IN A 10.8.1.3"),
		test.A("b-demo-0.svc.distro.local. 30 IN A 10.9.1.2"),
		test.A("b-demo-1.svc.distro.local. 30 IN A 10.9.1.3"),
	}

	expectedQuestions := map[string]Assertion{
		"_http._tcp.demo.svc.cluster-a.local.": assertion,
		"_http._tcp.demo.svc.cluster-b.local.": assertion,
	}
	answersFromCluster := map[string][]dns.RR{
		"_http._tcp.demo.svc.cluster-a.local.": {
			test.SRV("_http._tcp.demo.svc.cluster-a.local. 30 IN SRV 0 50 8080 demo-0.svc.cluster-a.local."),
			test.SRV("_http._tcp.demo.svc.cluster-a.local. 30 IN SRV 0 50 8080 demo-1.svc.cluster-a.local."),
		},
		"_http._tcp.demo.svc.cluster-b.local.": {
			test.SRV("_http._tcp.demo.svc.cluster-b.local. 30 IN SRV 0 50 8080 demo-0.svc.cluster-b.local."),
			test.SRV("_http._tcp.demo.svc.cluster-b.local. 30 IN SRV 0 50 8080 demo-1.svc.cluster-b.local."),
		},
	}
	extrasFromClusters := map[string][]dns.RR{
		"_http._tcp.demo.svc.cluster-a.local.": {
			test.A("demo-0.svc.cluster-a.local. 30 IN A 10.8.1.2"),
			test.A("demo-1.svc.cluster-a.local. 30 IN A 10.8.1.3"),
		},
		"_http._tcp.demo.svc.cluster-b.local.": {
			test.A("demo-0.svc.cluster-b.local. 30 IN A 10.9.1.2"),
			test.A("demo-1.svc.cluster-b.local. 30 IN A 10.9.1.3"),
		},
	}

	gatherPlugin := &GatherSrv{
		Next:   PrepareContentNextHandler(expectedQuestions, answersFromCluster, extrasFromClusters),
		Domain: "distro.local.",
		Clusters: []Cluster{
			{
				Suffix: "cluster-a.local.",
				Prefix: "a-",
			},
			{
				Suffix: "cluster-b.local.",
				Prefix: "b-",
			},
		},
	}
	msg := CheckAssertion(t, gatherPlugin, assertion)
	require.Equal(t, expectedAnswers, msg.Answer)
	require.Equal(t, expectedExtras, msg.Extra)
}

func PrepareOnlyCodeNextHandler(expectedQuestions map[string]Assertion) test.Handler {
	return test.HandlerFunc(func(ctx context.Context, w dns.ResponseWriter, r *dns.Msg) (int, error) {
		m := new(dns.Msg)
		if assertion, ok := expectedQuestions[r.Question[0].Name]; ok {
			m.SetRcode(r, int(assertion.ExpectedRcode))
			if err := w.WriteMsg(m); err !=nil {
				return dns.RcodeServerFailure, err
			}
			return int(assertion.ExpectedRcode), assertion.ExpectedError
		}
		m.SetRcode(r, dns.RcodeServerFailure)
		if err := w.WriteMsg(m); err !=nil {
			return dns.RcodeServerFailure, err
		}
		return dns.RcodeServerFailure, nil
	})
}

func PrepareContentNextHandler(expectedQuestions map[string]Assertion, answers map[string][]dns.RR, extras map[string][]dns.RR) test.Handler {
	return test.HandlerFunc(func(ctx context.Context, w dns.ResponseWriter, r *dns.Msg) (int, error) {
		m := new(dns.Msg)
		if assertion, ok := expectedQuestions[r.Question[0].Name]; ok {
			m.SetRcode(r, int(assertion.ExpectedRcode))
			m.Answer = answers[r.Question[0].Name]
			m.Extra = extras[r.Question[0].Name]
			if err := w.WriteMsg(m); err !=nil {
				return dns.RcodeServerFailure, err
			}
			return int(assertion.ExpectedRcode), assertion.ExpectedError
		}
		m.SetRcode(r, dns.RcodeServerFailure)
		if err := w.WriteMsg(m); err !=nil {
			return dns.RcodeServerFailure, err
		}
		return dns.RcodeServerFailure, nil
	})
}

func NewDnsMsg(assertion Assertion) *dns.Msg {
	req := new(dns.Msg)
	req.SetQuestion(dns.Fqdn(assertion.GivenName), assertion.GivenType)
	return req
}

func CheckAssertion(t *testing.T, srv *GatherSrv, assertion Assertion) *dns.Msg {
	ctx := context.TODO()
	rec := dnstest.NewRecorder(&test.ResponseWriter{})

	_, err := srv.ServeDNS(ctx, rec, NewDnsMsg(assertion))

	require.Equalf(t, assertion.ExpectedError, err, "Expected error %v, but got %v - assertion: %v", assertion.ExpectedError, err, assertion)
	require.Equalf(t, int(assertion.ExpectedRcode), rec.Msg.Rcode, "Expected status code %d, but got %d - assertion %v", assertion.ExpectedRcode, rec.Msg.Rcode, assertion)
	return rec.Msg
}
