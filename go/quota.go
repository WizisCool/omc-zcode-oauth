package main

import (
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

func clamp(v float64) float64 { return math.Max(0, math.Min(1, v)) }
func secondsTime(v number) string {
	if !v.Known || v.Value <= 0 {
		return ""
	}
	return time.Unix(int64(v.Value), 0).UTC().Format(time.RFC3339)
}
func metric(k, label, unit string, value float64) pluginapi.QuotaMetric {
	return pluginapi.QuotaMetric{Key: k, Label: label, Value: value, Unit: unit, Format: "number"}
}
func quotaFetch(req pluginapi.QuotaFetchRequest, sender httpSender) (pluginapi.QuotaFetchResponse, error) {
	c, err := readCredential(req.StorageJSON)
	if err != nil {
		return pluginapi.QuotaFetchResponse{}, err
	}
	out := pluginapi.QuotaFetchResponse{}
	if c.Plan != "" {
		tier := "Coding Plan"
		if c.Key == "" {
			tier = "Gift"
		}
		out.Subscription = &pluginapi.QuotaSubscription{Plan: c.Plan, TierName: tier}
	}
	if c.Key != "" {
		root := c.root()
		headers := teamHeaders(c, c.Org, c.Project)
		target := root + "/api/monitor/usage/quota/limit"
		if c.Org != "" && c.Project != "" {
			target += "?type=2"
		} else {
			headers = nil
			if c.Site == "bigmodel" {
				target = "https://open.bigmodel.cn/api/monitor/usage/quota/limit"
			}
		}
		if raw, e := apiCall(sender, "GET", target, c.Key, "", headers, nil); e == nil {
			var data struct {
				Limits []struct {
					Type       string  `json:"type"`
					Unit       int     `json:"unit"`
					Number     float64 `json:"number"`
					Percentage number  `json:"percentage"`
					NextReset  number  `json:"nextResetTime"`
				} `json:"limits"`
			}
			if json.Unmarshal(raw, &data) == nil {
				group := pluginapi.QuotaGroup{DisplayName: "GLM Coding Plan"}
				for _, limit := range data.Limits {
					if !limit.Percentage.Known {
						continue
					}
					name := ""
					desc := ""
					switch {
					case limit.Type == "TIME_LIMIT":
						name = "MCP"
						desc = "MCP usage; not a model limit"
					case limit.Type == "TOKENS_LIMIT" && limit.Unit == 3 && (limit.Number == 5 || limit.Number == 0):
						name = "5h"
					case limit.Type == "TOKENS_LIMIT" && ((limit.Unit == 6 && (limit.Number == 1 || limit.Number == 0)) || (limit.Unit == 4 && limit.Number == 7)):
						name = "Weekly"
					default:
						continue
					}
					reset := ""
					if limit.NextReset.Known && limit.NextReset.Value > 0 {
						reset = time.UnixMilli(int64(limit.NextReset.Value)).UTC().Format(time.RFC3339)
					}
					group.Buckets = append(group.Buckets, pluginapi.QuotaBucket{Window: name, RemainingFraction: clamp(1 - limit.Percentage.Value/100), ResetTime: reset, Description: desc})
				}
				if len(group.Buckets) > 0 {
					out.Groups = append(out.Groups, group)
				}
			}
		}
		// Team resets require a business token in the reference implementation;
		// the requested storage schema does not retain it. Do not guess its auth.
		if c.Org == "" && c.Project == "" {
			resetRoot := root
			if c.Site == "bigmodel" {
				resetRoot = "https://open.bigmodel.cn"
			}
			if raw, e := apiCall(sender, "GET", resetRoot+"/api/biz/customer-package-reset/list?targetType=PERSONAL", c.Key, "", nil, nil); e == nil {
				var d struct {
					Five []struct {
						Available bool `json:"available"`
					} `json:"fiveHourResets"`
					Weekly []struct {
						Available bool `json:"available"`
					} `json:"weekResets"`
				}
				if json.Unmarshal(raw, &d) == nil {
					f, w := 0, 0
					for _, x := range d.Five {
						if x.Available {
							f++
						}
					}
					for _, x := range d.Weekly {
						if x.Available {
							w++
						}
					}
					out.Summary = append(out.Summary, metric("five_hour_resets", "5h resets", "", float64(f)), metric("weekly_resets", "Weekly resets", "", float64(w)))
				}
			}
		}
	}
	if c.JWT != "" {
		if balance, e := fetchBalance(sender, c); e == nil {
			now := balance.now()
			if balance.ServerTime.Known {
				out.ServerTimeOffsetMs = int64(balance.ServerTime.Value*1000) - time.Now().UnixMilli()
			}
			group := pluginapi.QuotaGroup{DisplayName: "Gift plans"}
			total := float64(0)
			allKnown := true
			for _, x := range balance.Balances {
				for _, p := range balance.Plans {
					if !samePlan(p, x) {
						continue
					}
					active := livePlan(p, now)
					pending := pendingPlan(p, now)
					if !active && !pending {
						continue
					}
					if active && !bucketUsable(x, now) {
						continue
					}
					remaining := x.Remaining
					if !remaining.Known && x.Total.Known && x.Used.Known {
						remaining = number{Value: x.Total.Value - x.Used.Value, Known: true}
					}
					if active {
						if remaining.Known {
							total += math.Max(0, remaining.Value)
						} else {
							allKnown = false
						}
					}
					if !x.Total.Known || x.Total.Value <= 0 || !remaining.Known {
						continue
					}
					fraction := clamp(remaining.Value / x.Total.Value)
					desc := "Models: " + strings.Join(bucketModels(x), ", ")
					if pending {
						fraction = 0
						desc += "; 未开始"
					}
					for _, model := range bucketModels(x) {
						group.Buckets = append(group.Buckets, pluginapi.QuotaBucket{Window: p.Name + " · " + model, RemainingFraction: fraction, ResetTime: secondsTime(x.Expires), Description: desc})
					}
				}
			}
			if len(group.Buckets) > 0 {
				out.Groups = append(out.Groups, group)
			}
			if allKnown {
				out.Summary = append(out.Summary, metric("gift_tokens_remaining", "Gift tokens remaining", "tokens", total))
			}
			if out.Subscription == nil && balance.activePlanName() != "" {
				out.Subscription = &pluginapi.QuotaSubscription{Plan: balance.activePlanName(), TierName: "Gift"}
			}
		}
		target := zcodeRoot + "/api/v1/zcode-plan/billing/preview?" + url.Values{"app_version": {getConfig().AppVersion}, "platform": {zcodePlatform()}}.Encode()
		if raw, e := apiCall(sender, "GET", target, "Bearer "+c.JWT, c.Device, nil, nil); e == nil {
			var preview struct {
				Plans []struct {
					ID string `json:"plan_id"`
				} `json:"plans"`
			}
			if json.Unmarshal(raw, &preview) == nil {
				n := 0
				for _, p := range preview.Plans {
					if strings.TrimSpace(p.ID) != "" {
						n++
					}
				}
				out.Summary = append(out.Summary, metric("pending_claims", "Plans to claim", "", float64(n)))
			}
		}
	}
	if out.Subscription == nil {
		out.Subscription = &pluginapi.QuotaSubscription{Plan: "ZCode", TierName: "Gift"}
	}
	return out, nil
}
func quotaDescription() pluginapi.QuotaDescribeResponse {
	return pluginapi.QuotaDescribeResponse{SupportedProviders: []string{"zcode"}, DisplayName: "ZCode", SupportsReset: false}
}
func quotaReset() pluginapi.QuotaResetResponse {
	return pluginapi.QuotaResetResponse{Success: false, Message: fmt.Sprintf("Reset quota on the vendor website")}
}
