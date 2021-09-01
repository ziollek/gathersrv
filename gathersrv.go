package gathersrv

import (
	"context"
	"fmt"
	"github.com/coredns/coredns/plugin"
	"github.com/coredns/coredns/plugin/metrics"
	"github.com/coredns/coredns/plugin/pkg/log"
	"github.com/coredns/coredns/request"
	"github.com/miekg/dns"
	"strings"
)

var proxyTypes = [...]uint16{dns.TypeSRV, dns.TypeA, dns.TypeAAAA, dns.TypeTXT}

type Cluster struct {
	Suffix string
	Prefix string
}

type GatherSrv struct {
	Next     plugin.Handler
	Domain   string
	Clusters []Cluster
}

func (gatherSrv GatherSrv) ServeDNS(ctx context.Context, w dns.ResponseWriter, r *dns.Msg) (int, error) {
	var code int
	var err error
	state := request.Request{W: w, Req: r}
	if !gatherSrv.IsQualifiedQuestion(state.Req.Question[0]) {
		unqualifiedRequestCount.WithLabelValues(metrics.WithServer(ctx)).Inc()
		return plugin.NextOrFailure(gatherSrv.Name(), gatherSrv.Next, ctx, w, r)
	}

	qualifiedRequestCount.WithLabelValues(metrics.WithServer(ctx)).Inc()

	pw := NewResponsePrinter(w, r, gatherSrv.Domain, gatherSrv.Clusters)
	question := state.Req.Question[0].Name
	protocolPrefix, questionWithoutPrefix := divideDomain(question)

	isSend := false
	// TODO: clean up unnecessary logs
	log.Infof("question type: %d, class: %d", state.Req.Question[0].Qtype, state.Req.Question[0].Qclass)

	// TODO: parallel proxy requests
	for _, cluster := range gatherSrv.Clusters {
		if strings.HasPrefix(questionWithoutPrefix, cluster.Prefix) {
			state.Req.Question[0].Name = protocolPrefix + strings.Replace(
				strings.TrimPrefix(questionWithoutPrefix, cluster.Prefix), gatherSrv.Domain, cluster.Suffix, 1,
			)
			log.Infof("AAAAAASKs about: %s", state.Req.Question[0].Name)
			pw.counter = 1
			code, err = plugin.NextOrFailure(gatherSrv.Name(), gatherSrv.Next, ctx, pw, r)
			isSend = true
		}
	}
	if !isSend {
		for _, cluster := range gatherSrv.Clusters {
			state.Req.Question[0].Name = strings.Replace(question, gatherSrv.Domain, cluster.Suffix, 1)
			log.Debugf("Question name: %v", state.Req.Question[0].Name)
			code, err = plugin.NextOrFailure(gatherSrv.Name(), gatherSrv.Next, ctx, pw, r)
			log.Debugf("Result: %v %v", code, err)
			isSend = true
		}
	}
	if !isSend {
		code, err = plugin.NextOrFailure(gatherSrv.Name(), gatherSrv.Next, ctx, w, r)
	}
	return code, err
}

func (gatherSrv GatherSrv) IsQualifiedQuestion(question dns.Question) bool {
	return IsProxyType(question.Qtype) && strings.HasSuffix(question.Name, gatherSrv.Domain)
}

// Name implements the Handler interface.
func (gatherSrv GatherSrv) Name() string { return gatherSrvPluginName }

// Ready implements ready.Readiness interface
func (gatherSrv GatherSrv) Ready() bool { return true }

type GatherResponsePrinter struct {
	originalQuestion dns.Question
	domain           string
	counter          int
	clusters         []Cluster
	state            *dns.Msg
	dns.ResponseWriter
}

// NewResponsePrinter returns ResponseWriter.
func NewResponsePrinter(w dns.ResponseWriter, r *dns.Msg, domain string, clusters []Cluster) *GatherResponsePrinter {
	return &GatherResponsePrinter{
		ResponseWriter:   w,
		originalQuestion: r.Question[0],
		domain:           domain,
		clusters:         clusters,
		counter:          len(clusters),
		state:            nil,
	}
}

func (r *GatherResponsePrinter) WriteMsg(res *dns.Msg) error {
	if r.state == nil {
		r.state = res.Copy()
		r.state.Question[0] = r.originalQuestion
		r.state.Ns = []dns.RR{}
		r.state.Answer = []dns.RR{}
		r.state.Extra = []dns.RR{}
	}
	state := res.Copy()
	state.Question[0] = r.originalQuestion
	log.Infof("==============================gather, %v", res)
	for _, rr := range state.Ns {
		log.Infof("ns header: %v", rr.Header().Rrtype)
		log.Infof("header %s", rr.Header().Name)
	}
	for _, rr := range state.Answer {
		log.Infof("answer header: %v", rr.Header().Rrtype)
		r.Masquerade(rr)
		r.state.Answer = append(r.state.Answer, rr)

	}
	for _, rr := range state.Extra {
		if rr.Header().Rrtype == dns.TypeOPT {
			continue
		}
		log.Infof("extra header: %v", rr.Header().Rrtype)
		log.Infof("header %s", rr.Header().Name)
		log.Infof("header ptr %p", rr.Header())
		r.Masquerade(rr)
		r.state.Extra = append(r.state.Extra, rr)
	}

	if r.counter--; r.counter > 0 {
		return nil
	}
	for _, rr := range state.Extra {
		if rr.Header().Rrtype == dns.TypeOPT {
			r.state.Extra = append(r.state.Extra, rr)
		}
	}
	log.Infof("gather, %v", r.state)
	return r.ResponseWriter.WriteMsg(r.state)
}

func (r *GatherResponsePrinter) Masquerade(rr dns.RR) {
	// TODO: extract to specialized class
	log.Infof("header ptr %p", rr.Header())
	for _, cluster := range r.clusters {
		if strings.HasSuffix(rr.Header().Name, cluster.Suffix) {
			replaceHead, replaceTail := divideDomain(strings.Replace(rr.Header().Name, cluster.Suffix, r.domain, 1))
			switch rr.Header().Rrtype {
			case dns.TypeSRV:
				srvRecord := rr.(*dns.SRV)
				srvRecord.Header().Name = replaceHead + replaceTail
				head, tail := divideDomain(strings.Replace(srvRecord.Target, cluster.Suffix, r.domain, 1))
				srvRecord.Target = fmt.Sprintf("%s%s%s", head, cluster.Prefix, tail)
				log.Infof("SRV target %s", rr.(*dns.SRV).String())
				break
			case dns.TypeA:
				rr.Header().Name = fmt.Sprintf("%s%s%s", replaceHead, cluster.Prefix, replaceTail)
				log.Infof("A target %s", rr.(*dns.A).String())
				break
			case dns.TypeAAAA:
				rr.Header().Name = fmt.Sprintf("%s%s%s", replaceHead, cluster.Prefix, replaceTail)
				log.Infof("AAAA target %s", rr.(*dns.AAAA).String())
				break
			case dns.TypeOPT:
				// TODO: test case
				// do not merge OPT records
				break
			default:
				log.Infof("Unexpected type %v", rr.Header().Rrtype)
			}
		}
	}

}

func divideDomain(domain string) (string, string) {
	// TODO: move to util
	protocolPrefix := ""
	for _, element := range strings.Split(domain, ".") {
		if strings.HasPrefix(element, "_") {
			protocolPrefix = protocolPrefix + element + "."
		} else {
			break
		}
	}
	return protocolPrefix, strings.TrimPrefix(domain, protocolPrefix)
}

func IsProxyType(questionType uint16) bool {
	// TODO: move to util
	for _, proxyType := range proxyTypes {
		if proxyType == questionType {
			return true
		}
	}
	return false
}
