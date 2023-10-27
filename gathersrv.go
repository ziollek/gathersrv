package gathersrv

import (
	"context"
	"fmt"
	"github.com/coredns/coredns/plugin"
	"github.com/coredns/coredns/plugin/metrics"
	"github.com/coredns/coredns/plugin/pkg/log"
	"github.com/miekg/dns"
	"strings"
	"time"
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
	Code  int
	Err   error
	empty bool
}

func (nr *NextResp) Reduce(subsequentResponse *NextResp) {
	// return no error if at least one sub-request went well
	if nr.empty || (nr.Err != nil && subsequentResponse.Err == nil) {
		nr.empty = false
		nr.Err = subsequentResponse.Err
		nr.Code = subsequentResponse.Code
	}
}

type subRequest struct {
	prefix  string
	request *dns.Msg
}

func (gatherSrv GatherSrv) ServeDNS(ctx context.Context, w dns.ResponseWriter, r *dns.Msg) (int, error) {
	questionType := dns.Type(r.Question[0].Qtype).String()
	if !gatherSrv.IsQualifiedQuestion(r.Question[0]) {
		requestCount.WithLabelValues(metrics.WithServer(ctx), "false", questionType).Inc()
		return plugin.NextOrFailure(gatherSrv.Name(), gatherSrv.Next, ctx, w, r)
	}
	requestCount.WithLabelValues(metrics.WithServer(ctx), "true", questionType).Inc()

	// build proper number of sub-requests depends on defined clusters
	subRequests := gatherSrv.prepareSubRequests(r)
	respChan := make(chan *NextResp, len(subRequests))
	defer close(respChan)
	pw := NewResponsePrinter(w, r, gatherSrv.Domain, gatherSrv.Clusters, len(subRequests))

	// call sub-requests in parallel manner
	doSubRequest := func(ctx context.Context, pw dns.ResponseWriter, s *subRequest) {
		code, err := plugin.NextOrFailure(gatherSrv.Name(), gatherSrv.Next, ctx, pw, s.request)
		subRequestCount.WithLabelValues(metrics.WithServer(ctx), s.prefix, questionType, fmt.Sprintf("%d", code))
		if err != nil {
			log.Warningf(
				"Error occurred for: type=%s, question=%s, error=%s",
				questionType,
				s.request.Question[0].Name,
				err,
			)
		}
		respChan <- &NextResp{Code: code, Err: err}
	}
	for _, subRequestParams := range subRequests {
		go doSubRequest(ctx, pw, subRequestParams)
	}

	// gather all responses or return partial response on context done
	mergedResponse := &NextResp{empty: true}
	for waitCnt := len(subRequests); waitCnt > 0; waitCnt-- {
		select {
		case subResponse := <-respChan:
			mergedResponse.Reduce(subResponse)
		case <-ctx.Done():
			waitCnt = 0
		}
	}
	pw.Flush()
	return mergedResponse.Code, mergedResponse.Err
}

func (gatherSrv GatherSrv) prepareSubRequests(r *dns.Msg) (calls []*subRequest) {
	question := r.Question[0].Name
	protocolPrefix, questionWithoutPrefix := divideDomain(r.Question[0].Name)

	for _, cluster := range gatherSrv.Clusters {
		if strings.HasPrefix(questionWithoutPrefix, cluster.Prefix) {
			sr := r.Copy()
			sr.Question[0].Name = protocolPrefix + strings.Replace(
				strings.TrimPrefix(questionWithoutPrefix, cluster.Prefix), gatherSrv.Domain, cluster.Suffix, 1,
			)
			calls = append(calls, &subRequest{cluster.Prefix, sr})
		}
	}

	if len(calls) == 0 {
		for _, cluster := range gatherSrv.Clusters {
			sr := r.Copy()
			sr.Question[0].Name = strings.Replace(question, gatherSrv.Domain, cluster.Suffix, 1)
			calls = append(calls, &subRequest{cluster.Prefix, sr})
		}
	}
	log.Infof("calls %v, clusters %v", calls, gatherSrv.Clusters)
	return
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
	start            time.Time
	dns.ResponseWriter
}

// NewResponsePrinter returns ResponseWriter.
func NewResponsePrinter(w dns.ResponseWriter, r *dns.Msg, domain string, clusters []Cluster, counter int) *GatherResponsePrinter {
	return &GatherResponsePrinter{
		lockCh:           make(chan bool, 1),
		ResponseWriter:   w,
		originalQuestion: r.Question[0],
		domain:           domain,
		clusters:         clusters,
		counter:          counter,
		state:            nil,
		start:            time.Now(),
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
	return nil
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

func (w *GatherResponsePrinter) Flush() {
	w.lockCh <- true
	defer func() {
		<-w.lockCh
	}()
	if w.state != nil {
		if err := w.ResponseWriter.WriteMsg(w.state); err != nil {
			log.Errorf(
				"error occurred while writing response: question=%v, error=%s", w.originalQuestion, err,
			)
		}
	}
	w.shortMessage()
}

func (w *GatherResponsePrinter) shortMessage() {
	if w.state != nil {
		questionType := dns.Type(w.state.Question[0].Qtype).String()
		log.Infof(
			"type=%s, question=%s, response=%s, answer-records=%d, extra-records=%d, gathered=%d, not-gatherer=%d, duration=%s",
			questionType,
			w.state.Question[0].Name,
			strings.Split(w.state.MsgHdr.String(), "\n")[0],
			len(w.state.Answer),
			len(w.state.Extra),
			len(w.clusters)-w.counter,
			w.counter,
			time.Since(w.start),
		)
	} else {
		log.Errorf(
			"response printer has an empty state, original question was: %v", w.originalQuestion,
		)
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
