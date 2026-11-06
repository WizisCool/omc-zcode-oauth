package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// giftBalanceFixture is a balance answer with one live gift plan and one usable
// bucket, so the plan name is the only input that changes between cases.
func giftBalanceFixture(now int64, plan string) string {
	return fmt.Sprintf(`{"server_time":%d,"plans":[{"plan_id":"p1","user_plan_id":"u1","name":%q,"status":"active","starts_at":%d,"ends_at":%d}],"balances":[{"plan_id":"p1","user_plan_id":"u1","capabilities":["model:glm-5.3-flash"],"total_units":100,"used_units":25,"remaining_units":75,"expires_at":%d}]}`,
		now, plan, now-60, now+3600, now+3600)
}

func giftQuotaSender(body string) *fakeSender {
	return &fakeSender{reply: func(r pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
		switch {
		case strings.Contains(r.URL, "/billing/balance"):
			return jsonReply(body), nil
		case strings.Contains(r.URL, "/billing/preview"):
			return jsonReply(`{"plans":[]}`), nil
		default:
			return pluginapi.HTTPResponse{}, fmt.Errorf("unexpected quota request %s", r.URL)
		}
	}}
}

// OMC renders a bucket as "<group> · <window>". An activation code is an
// identifier, not display text, so the window keeps the model and names the
// plan by its role instead.
func TestQuotaGiftWindowsHideOpaquePlanNames(t *testing.T) {
	cases := []struct {
		name   string
		plan   string
		window string
	}{
		{name: "human plan name is kept", plan: "ZCODE GLOBAL BUILD", window: "ZCODE GLOBAL BUILD · glm-5.3-flash"},
		{name: "activation code falls back to the gift role", plan: "act-20261105-316", window: "Gift · glm-5.3-flash"},
		{name: "uuid falls back to the gift role", plan: "3f2a6c1e-1234-4abc-9def-0123456789ab", window: "Gift · glm-5.3-flash"},
		{name: "hex id falls back to the gift role", plan: "8f7c2d1e6a4b9c0d", window: "Gift · glm-5.3-flash"},
		{name: "bare number falls back to the gift role", plan: "20261105", window: "Gift · glm-5.3-flash"},
	}
	now := time.Now().Unix()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp, err := quotaFetch(pluginapi.QuotaFetchRequest{StorageJSON: storage(credential{Site: "zai", JWT: "fixture-jwt", Device: "fixture-device"})}, giftQuotaSender(giftBalanceFixture(now, c.plan)))
			if err != nil {
				t.Fatal(err)
			}
			if len(resp.Groups) != 1 || resp.Groups[0].DisplayName != "ZCode" || len(resp.Groups[0].Buckets) != 1 {
				t.Fatalf("groups = %+v", resp.Groups)
			}
			bucket := resp.Groups[0].Buckets[0]
			if bucket.Window != c.window {
				t.Errorf("window = %q, want %q", bucket.Window, c.window)
			}
			if bucket.RemainingFraction != 0.75 {
				t.Errorf("remainingFraction = %v, want 0.75", bucket.RemainingFraction)
			}
		})
	}
}

// The exact amounts lead the gift description because that line is the only
// place a host can read the counts: a bar alone cannot say how much is left.
func TestQuotaGiftDescriptionsLeadWithAmounts(t *testing.T) {
	cases := []struct {
		name string
		plan string
		want string
	}{
		{name: "human plan", plan: "ZCODE GLOBAL BUILD", want: "75 / 100 tokens left; Models: glm-5.3-flash"},
		{name: "opaque plan", plan: "act-20261105-316", want: "75 / 100 tokens left; Models: glm-5.3-flash"},
	}
	now := time.Now().Unix()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp, err := quotaFetch(pluginapi.QuotaFetchRequest{StorageJSON: storage(credential{Site: "zai", JWT: "fixture-jwt", Device: "fixture-device"})}, giftQuotaSender(giftBalanceFixture(now, c.plan)))
			if err != nil {
				t.Fatal(err)
			}
			desc := resp.Groups[0].Buckets[0].Description
			if !strings.HasPrefix(desc, "75 / 100 tokens left") {
				t.Fatalf("description does not begin with the amount: %q", desc)
			}
			if desc != c.want {
				t.Errorf("description = %q, want %q", desc, c.want)
			}
		})
	}
}

func TestOpaquePlanName(t *testing.T) {
	cases := map[string]bool{
		"act-20261105-316":                     true,
		"plan_123456":                          true,
		"20261105":                             true,
		"8f7c2d1e6a4b9c0d":                     true,
		"3f2a6c1e-1234-4abc-9def-0123456789ab": true,
		"ZCODE GLOBAL BUILD":                   false,
		"Coding Pro":                           false,
		"Gift plans":                           false,
		"GLM-5.3-Flash":                        false,
		"":                                     false,
	}
	for name, want := range cases {
		if got := opaquePlanName(name); got != want {
			t.Errorf("opaquePlanName(%q) = %v, want %v", name, got, want)
		}
	}
}

// Every summary row needs its own stable key; a repeated "credits_left" hides
// one pool behind another. Percentage-only windows contribute reset counts, not
// invented token totals.
func TestQuotaSummaryKeysAreUniqueAndStable(t *testing.T) {
	now := time.Now().Unix()
	sender := &fakeSender{reply: func(r pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
		switch {
		case strings.Contains(r.URL, "/usage/quota/limit"):
			return jsonReply(fmt.Sprintf(`{"limits":[{"type":"TOKENS_LIMIT","unit":3,"number":5,"percentage":20,"nextResetTime":%d},{"type":"TOKENS_LIMIT","unit":6,"number":1,"percentage":60},{"type":"TIME_LIMIT","unit":5,"number":1,"percentage":25}]}`, (now+3600)*1000)), nil
		case strings.Contains(r.URL, "customer-package-reset/list"):
			return jsonReply(`{"fiveHourResets":[{"available":true},{"available":false}],"weekResets":[{"available":true}]}`), nil
		case strings.Contains(r.URL, "billing/balance"):
			return jsonReply(fixtureBalance(now)), nil
		case strings.Contains(r.URL, "billing/preview"):
			return jsonReply(`{"plans":[{"plan_id":"a"},{"plan_id":"b"}]}`), nil
		default:
			return pluginapi.HTTPResponse{}, fmt.Errorf("unexpected quota request %s", r.URL)
		}
	}}
	q, err := quotaFetch(pluginapi.QuotaFetchRequest{StorageJSON: storage(credential{Site: "zai", Key: "fixture-key", JWT: "fixture-jwt", Device: "device", Plan: "Coding Pro", UID: "id"})}, sender)
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]float64{}
	for _, m := range q.Summary {
		if m.Key == "" {
			t.Fatalf("summary metric %q has no key", m.Label)
		}
		if _, seen := values[m.Key]; seen {
			t.Fatalf("summary key %q is repeated", m.Key)
		}
		values[m.Key] = m.Value
	}
	for k, want := range map[string]float64{"five_hour_resets": 1, "weekly_resets": 1, "gift_tokens_remaining": 75, "pending_claims": 2} {
		if got, ok := values[k]; !ok || got != want {
			t.Errorf("summary %s = %v (present %v), want %v", k, got, ok, want)
		}
	}
	if len(q.Groups) != 1 {
		t.Fatalf("groups = %+v", q.Groups)
	}
	// The 5h and weekly buckets come from percentages; their descriptions must
	// not claim a token total.
	for _, b := range q.Groups[0].Buckets {
		if (b.Window == "5h" || b.Window == "Weekly") && strings.Contains(b.Description, "/") {
			t.Errorf("%s description invents an amount: %q", b.Window, b.Description)
		}
	}
}

// The same sanitising applies to the subscription label plans.go derives from a
// gift balance, not only to the bucket windows.
func TestActivePlanNameHidesOpaqueNames(t *testing.T) {
	now := time.Now().Unix()
	cases := []struct {
		name string
		plan string
		want string
	}{
		{name: "human plan name", plan: "ZCODE GLOBAL BUILD", want: "ZCODE GLOBAL BUILD"},
		{name: "activation code", plan: "act-20261105-316", want: "Gift"},
		{name: "empty plan name", plan: "", want: "Gift"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			balance := balanceData{Plans: []planInfo{{Name: c.plan, Status: "active", Starts: number{Value: float64(now - 60), Known: true}, Ends: number{Value: float64(now + 3600), Known: true}}}}
			if got := balance.activePlanName(); got != c.want {
				t.Errorf("activePlanName() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestFormatAmountWritesGiftCounts(t *testing.T) {
	cases := map[float64]string{
		0:       "0",
		120:     "120",
		2000:    "2,000",
		1234567: "1,234,567",
		1234.5:  "1,234.5",
	}
	for value, want := range cases {
		if got := formatAmount(value); got != want {
			t.Errorf("formatAmount(%v) = %q, want %q", value, got, want)
		}
	}
}
