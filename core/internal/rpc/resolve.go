package rpc

import (
	"ThroneCore/internal/tachyon"
	"context"
	"time"

	"ThroneCore/gen"
	"ThroneCore/internal/resolve"
)

func (s *server) ResolveDomains(ctx context.Context, in *gen.ResolveDomainsRequest) (*gen.ResolveDomainsResponse, error) {
	native := currentNative()
	if native == nil && tachyon.Selected([]byte(in.GetConfig())) {
		var err error
		native, err = newNativeBox(ctx, in.GetConfig())
		if err != nil {
			return nil, err
		}
		defer native.engine.Stop()
	}
	if native != nil {
		response := &gen.ResolveDomainsResponse{}
		for _, domain := range in.GetDomains() {
			lookup, cancel := context.WithTimeout(ctx, 5*time.Second)
			addresses, err := native.engine.Resolve(lookup, domain)
			cancel()
			item := &gen.ResolvedDomain{Domain: To(domain), Addresses: addresses}
			if err != nil {
				item.Error = To(err.Error())
			}
			response.Results = append(response.Results, item)
		}
		return response, nil
	}
	var running context.Context
	if box := currentBox(); box != nil {
		running = box.Context()
	}
	results, err := resolve.Domains(running, func() (context.Context, func(), error) {
		env, err := prepareTestEnv(false, false, "", nil, in.GetConfig(), nil, false, "")
		if err != nil {
			return nil, nil, err
		}
		return env.box.Context(), env.close, nil
	}, in.GetDomains())
	if err != nil {
		return nil, err
	}
	response := &gen.ResolveDomainsResponse{}
	for _, result := range results {
		resolved := &gen.ResolvedDomain{Domain: To(result.Domain), Addresses: result.Addresses}
		if result.Error != nil {
			resolved.Error = To(result.Error.Error())
		}
		response.Results = append(response.Results, resolved)
	}
	return response, nil
}
