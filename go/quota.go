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
	// OMC renders a bucket as "<group display name> · <window>" and prints the
	// bucket description beneath it, so the group carries the product, each
	// window names its own pool, and the description carries the exact amounts.
	group := pluginapi.QuotaGroup{DisplayName: "ZCode"}
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
						// The vendor reports a percentage only, so the description
						// names the window instead of inventing token totals.
						name = "5h"
						desc = "Coding Plan 5-hour window"
					case limit.Type == "TOKENS_LIMIT" && ((limit.Unit == 6 && (limit.Number == 1 || limit.Number == 0)) || (limit.Unit == 4 && limit.Number == 7)):
						name = "Weekly"
						desc = "Coding Plan weekly window"
					default:
						continue
					}
					reset := ""
					if limit.NextReset.Known && limit.NextReset.Value > 0 {
						reset = time.UnixMilli(int64(limit.NextReset.Value)).UTC().Format(time.RFC3339)
					}
					group.Buckets = append(group.Buckets, pluginapi.QuotaBucket{Window: name, RemainingFraction: clamp(1 - limit.Percentage.Value/100), ResetTime: reset, Description: desc})
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
					// The amounts lead: the description is the only place a host can
					// read the counts, and OMC prints it under the bar.
					desc := fmt.Sprintf("%s / %s tokens left", formatAmount(math.Max(0, remaining.Value)), formatAmount(x.Total.Value))
					desc += "; Models: " + strings.Join(bucketModels(x), ", ")
					if pending {
						fraction = 0
						desc += "; 未开始"
					}
					for _, model := range bucketModels(x) {
						group.Buckets = append(group.Buckets, pluginapi.QuotaBucket{Window: displayPlanName(p.Name) + " · " + model, RemainingFraction: fraction, ResetTime: secondsTime(x.Expires), Description: desc})
					}
				}
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
	if len(group.Buckets) > 0 {
		out.Groups = append(out.Groups, group)
	}
	if out.Subscription == nil {
		out.Subscription = &pluginapi.QuotaSubscription{Plan: "ZCode", TierName: "Gift"}
	}
	return out, nil
}

// opaquePlanName reports a vendor plan name that carries an identifier rather
// than display text: activation codes ("act-20261105-316"), UUIDs, hex ids and
// bare numbers. Those must never become a quota window or subscription label.
func opaquePlanName(name string) bool {
	n := strings.TrimSpace(name)
	if n == "" {
		return false
	}
	if strings.Count(n, "-") == 4 && len(n) == 36 {
		return true
	}
	digits, letters := 0, 0
	for _, r := range n {
		switch {
		case r >= '0' && r <= '9':
			digits++
		case r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z':
			letters++
		}
	}
	if digits == 0 {
		return false
	}
	// "act-20261105-316", "plan_123456", "sub-0001": a short word then a run
	// of digits, with no spaces.
	if !strings.ContainsAny(n, " \t") && letters <= 8 {
		parts := strings.FieldsFunc(n, func(r rune) bool { return r == '-' || r == '_' })
		if len(parts) >= 2 && digits >= 6 {
			for _, part := range parts[1:] {
				if part == "" {
					return false
				}
				for _, r := range part {
					if r < '0' || r > '9' {
						return false
					}
				}
			}
			return true
		}
	}
	if digits == len([]rune(n)) && digits >= 6 {
		return true
	}
	hexOnly := true
	for _, r := range strings.ToLower(n) {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			hexOnly = false
			break
		}
	}
	return hexOnly && len(n) >= 16
}

// displayPlanName is the plan half of a quota label: the vendor's own name when
// it reads as display text, otherwise the role the pool plays.
func displayPlanName(name string) string {
	if n := strings.TrimSpace(name); n != "" && !opaquePlanName(n) {
		return n
	}
	return "Gift"
}

// formatAmount writes a unit count with thousands separators and one decimal
// for a part unit, matching how the vendor panels show gift amounts.
func formatAmount(v float64) string {
	var text string
	if v == math.Trunc(v) {
		text = fmt.Sprintf("%.0f", v)
	} else {
		text = fmt.Sprintf("%.1f", v)
	}
	whole, fraction, hasFraction := strings.Cut(text, ".")
	var b strings.Builder
	for i := 0; i < len(whole); i++ {
		if i > 0 && (len(whole)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteByte(whole[i])
	}
	if hasFraction {
		b.WriteByte('.')
		b.WriteString(fraction)
	}
	return b.String()
}
func quotaDescription() pluginapi.QuotaDescribeResponse {
	return pluginapi.QuotaDescribeResponse{SupportedProviders: []string{"zcode"}, DisplayName: "ZCode", SupportsReset: false}
}
func quotaReset() pluginapi.QuotaResetResponse {
	return pluginapi.QuotaResetResponse{Success: false, Message: fmt.Sprintf("Reset quota on the vendor website")}
}
