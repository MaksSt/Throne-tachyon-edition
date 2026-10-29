package rpc

import (
	"context"
	"encoding/json"
	"net/url"

	"ThroneCore/gen"
	"ThroneCore/internal/rulesets"
)

func (s *server) UpdateRuleSets(ctx context.Context, in *gen.EmptyReq) (*gen.UpdateRuleSetsResponse, error) {
	if native := currentNative(); native != nil {
		var providers struct {
			Providers json.RawMessage `json:"providers"`
		}
		if err := native.engine.Call(ctx, "GET", "/providers/rules", nil, &providers); err != nil {
			return &gen.UpdateRuleSetsResponse{Error: To(err.Error())}, nil
		}
		var updated int32
		names := map[string]any{}
		if err := json.Unmarshal(providers.Providers, &names); err != nil {
			var list []struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal(providers.Providers, &list); err != nil {
				return &gen.UpdateRuleSetsResponse{Error: To("invalid Tachyon rule provider response")}, nil
			}
			for _, item := range list {
				names[item.Name] = nil
			}
		}
		for name := range names {
			if err := native.engine.Call(ctx, "PUT", "/providers/rules/"+url.PathEscape(name), nil, nil); err != nil {
				return &gen.UpdateRuleSetsResponse{Updated: To(updated), Error: To(err.Error())}, nil
			}
			updated++
		}
		return &gen.UpdateRuleSetsResponse{Updated: To(updated)}, nil
	}
	box := currentBox()
	if box == nil {
		return &gen.UpdateRuleSetsResponse{Error: To("no instance is running")}, nil
	}
	updated, errorText := rulesets.UpdateAll(ctx, box.Router())
	return &gen.UpdateRuleSetsResponse{Updated: To(updated), Error: To(errorText)}, nil
}
