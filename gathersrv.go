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
	"sync"
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

type NextResp struct {
	Code int
	Err  error
}

func (gatherSrv GatherSrv) ServeDNS(ctx context.Context, w dns.ResponseWriter, r *dns.Msg) (int, error) {
	var wg sync.WaitGroup

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
	respChan := make(chan NextResp, len(gatherSrv.Clusters))

	// TODO: parallel proxy requests
	for _, cluster := range gatherSrv.Clusters {
		if strings.HasPrefix(questionWithoutPrefix, cluster.Prefix) {
			state.Req.Question[0].Name = protocolPrefix + strings.Replace(
				strings.TrimPrefix(questionWithoutPrefix, cluster.Prefix), gatherSrv.Domain, cluster.Suffix, 1,
			)
			pw.counter = 1
			wg.Add(1)
			go func(ctx context.Context, w dns.ResponseWriter, r *dns.Msg) {
				code, err := plugin.NextOrFailure(gatherSrv.Name(), gatherSrv.Next, ctx, pw, r)
				respChan <- NextResp{Code: code, Err: err}
				wg.Done()
			}(ctx, pw, r.Copy())
			isSend = true
		}
	}
	if !isSend {
		for _, cluster := range gatherSrv.Clusters {
			state.Req.Question[0].Name = strings.Replace(question, gatherSrv.Domain, cluster.Suffix, 1)
			wg.Add(1)
			go func(ctx context.Context, w dns.ResponseWriter, r *dns.Msg) {
				code, err := plugin.NextOrFailure(gatherSrv.Name(), gatherSrv.Next, ctx, pw, r)
				respChan <- NextResp{Code: code, Err: err}
				wg.Done()
			}(ctx, pw, r.Copy())
			isSend = true
		}
	}
	if !isSend {
		close(respChan)
		return plugin.NextOrFailure(gatherSrv.Name(), gatherSrv.Next, ctx, w, r)
	} else {
		wg.Wait()
		r := <-respChan
		close(respChan)
		// todo - figure out better way to merge response codes & errors
		return r.Code, r.Err
	}
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
	lockCh           chan bool
	domain           string
	counter          int
	clusters         []Cluster
	state            *dns.Msg
	dns.ResponseWriter
}

// NewResponsePrinter returns ResponseWriter.
func NewResponsePrinter(w dns.ResponseWriter, r *dns.Msg, domain string, clusters []Cluster) *GatherResponsePrinter {
	return &GatherResponsePrinter{
		lockCh:           make(chan bool, 1),
		ResponseWriter:   w,
		originalQuestion: r.Question[0],
		domain:           domain,
		clusters:         clusters,
		counter:          len(clusters),
		state:            nil,
	}
}

func (w *GatherResponsePrinter) WriteMsg(res *dns.Msg) error {
	w.lockCh <- true
	defer func() {
		<-w.lockCh
	}()
	state := res.Copy()
	if w.state == nil {
		w.state = res.Copy()
		w.state.Question[0] = w.originalQuestion
		w.state.Ns = []dns.RR{}
		w.state.Answer = []dns.RR{}
		w.state.Extra = []dns.RR{}
	} else {
		if state.Rcode == dns.RcodeSuccess && w.state.Rcode != dns.RcodeSuccess {
			w.state.Rcode = state.Rcode
			w.state.RecursionAvailable = state.RecursionAvailable
			w.state.Authoritative = state.Authoritative
			w.state.Truncated = state.Truncated
		}
	}

	state.Question[0] = w.originalQuestion
	for _, rr := range state.Answer {
		w.Masquerade(rr)
		w.state.Answer = append(w.state.Answer, rr)

	}
	for _, rr := range state.Extra {
		if rr.Header().Rrtype == dns.TypeOPT {
			continue
		}
		w.Masquerade(rr)
		w.state.Extra = append(w.state.Extra, rr)
	}

	if w.counter--; w.counter > 0 {
		return nil
	}
	for _, rr := range state.Extra {
		if rr.Header().Rrtype == dns.TypeOPT {
			w.state.Extra = append(w.state.Extra, rr)
		}
	}
	log.Infof("gather, %v", w.state)
	return w.ResponseWriter.WriteMsg(w.state)
}

func (w *GatherResponsePrinter) Masquerade(rr dns.RR) {
	// TODO: extract to specialized class
	for _, cluster := range w.clusters {
		if strings.HasSuffix(rr.Header().Name, cluster.Suffix) {
			replaceHead, replaceTail := divideDomain(strings.Replace(rr.Header().Name, cluster.Suffix, w.domain, 1))
			switch rr.Header().Rrtype {
			case dns.TypeSRV:
				srvRecord := rr.(*dns.SRV)
				srvRecord.Header().Name = replaceHead + replaceTail
				head, tail := divideDomain(strings.Replace(srvRecord.Target, cluster.Suffix, w.domain, 1))
				srvRecord.Target = fmt.Sprintf("%s%s%s", head, cluster.Prefix, tail)
			case dns.TypeA:
				rr.Header().Name = fmt.Sprintf("%s%s%s", replaceHead, cluster.Prefix, replaceTail)
			case dns.TypeAAAA:
				rr.Header().Name = fmt.Sprintf("%s%s%s", replaceHead, cluster.Prefix, replaceTail)
			case dns.TypeOPT:
				// TODO: test case
				// do not merge OPT records
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
