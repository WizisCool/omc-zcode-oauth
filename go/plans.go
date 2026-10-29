package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type flexString string

func (s *flexString) UnmarshalJSON(b []byte) error {
	var v string
	if err := json.Unmarshal(b, &v); err == nil {
		*s = flexString(v)
		return nil
	}
	if string(b) == "null" {
		*s = ""
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(b, &n); err != nil {
		return err
	}
	*s = flexString(n.String())
	return nil
}

type customer struct {
	Organizations []struct {
		ID       flexString `json:"organizationId"`
		Name     string     `json:"organizationName"`
		Projects []struct {
			ID   flexString `json:"projectId"`
			Name string     `json:"projectName"`
			Type flexString `json:"projectType"`
		} `json:"projects"`
	} `json:"organizations"`
}
type subscription struct {
	Status  string `json:"status"`
	Product string `json:"productName"`
}
type teamDetail struct {
	Has     *bool  `json:"hasSubscription"`
	Status  string `json:"status"`
	Grant   string `json:"memberGrantStatus"`
	Product string `json:"productName"`
}
type planInfo struct {
	PlanID     flexString `json:"plan_id"`
	UserPlanID flexString `json:"user_plan_id"`
	Name       string     `json:"name"`
	Status     string     `json:"status"`
	Starts     number     `json:"starts_at"`
	Ends       number     `json:"ends_at"`
}
type giftBucket struct {
	PlanID        flexString `json:"plan_id"`
	UserPlanID    flexString `json:"user_plan_id"`
	EntitlementID flexString `json:"entitlement_id"`
	Capabilities  []string   `json:"capabilities"`
	Total         number     `json:"total_units"`
	Used          number     `json:"used_units"`
	Remaining     number     `json:"remaining_units"`
	Expires       number     `json:"expires_at"`
	PeriodStart   number     `json:"period_start"`
	PeriodEnd     number     `json:"period_end"`
	ShowName      string     `json:"show_name"`
}
type balanceData struct {
	ServerTime number       `json:"server_time"`
	Plans      []planInfo   `json:"plans"`
	Balances   []giftBucket `json:"balances"`
}
type number struct {
	Value float64
	Known bool
}

func (n *number) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		return nil
	}
	var v float64
	if json.Unmarshal(b, &v) == nil {
		n.Value = v
		n.Known = true
		return nil
	}
	var s string
	if json.Unmarshal(b, &s) == nil {
		v, err := strconv.ParseFloat(s, 64)
		if err == nil {
			n.Value = v
			n.Known = true
			return nil
		}
	}
	return errors.New("invalid numeric upstream field")
}
func fetchBalance(sender httpSender, c credential) (balanceData, error) {
	var out balanceData
	if c.JWT == "" {
		return out, errors.New("ZCode sign-in required")
	}
	if exp := jwtExpiry(c.JWT); exp > 0 && time.Now().Unix() >= exp {
		return out, errors.New("ZCode sign-in expired; sign in again")
	}
	target := zcodeRoot + "/api/v1/zcode-plan/billing/balance?" + url.Values{"app_version": {getConfig().AppVersion}}.Encode()
	b, err := apiCall(sender, "GET", target, "Bearer "+c.JWT, c.Device, nil, nil)
	if err != nil {
		return out, err
	}
	if json.Unmarshal(b, &out) != nil {
		return out, errors.New("invalid gift balance")
	}
	return out, nil
}
func (b balanceData) now() float64 {
	if b.ServerTime.Known && b.ServerTime.Value > 0 {
		return b.ServerTime.Value
	}
	return float64(time.Now().Unix())
}
func samePlan(p planInfo, x giftBucket) bool {
	if p.UserPlanID != "" && x.UserPlanID != "" {
		return p.UserPlanID == x.UserPlanID
	}
	return p.PlanID != "" && p.PlanID == x.PlanID
}
func livePlan(p planInfo, now float64) bool {
	return strings.EqualFold(p.Status, "active") && p.Starts.Known && p.Ends.Known && p.Starts.Value <= now && p.Ends.Value > now
}
func pendingPlan(p planInfo, now float64) bool {
	return strings.EqualFold(p.Status, "active") && p.Starts.Known && p.Starts.Value > now && (!p.Ends.Known || p.Ends.Value > now)
}
func bucketUsable(x giftBucket, now float64) bool {
	if x.Expires.Known && x.Expires.Value > 0 && x.Expires.Value <= now {
		return false
	}
	if x.Remaining.Known {
		return x.Remaining.Value > 0
	}
	return !(x.Used.Known && x.Total.Known && x.Used.Value >= x.Total.Value)
}
func bucketModels(x giftBucket) []string {
	var out []string
	for _, v := range x.Capabilities {
		v = strings.TrimSpace(v)
		if strings.HasPrefix(v, "model:") {
			v = strings.TrimSpace(strings.TrimPrefix(v, "model:"))
			if v != "" {
				out = append(out, v)
			}
		}
	}
	return out
}
func giftHasModel(b balanceData, model string) bool {
	now := b.now()
	for _, x := range b.Balances {
		if !bucketUsable(x, now) {
			continue
		}
		for _, p := range b.Plans {
			if livePlan(p, now) && samePlan(p, x) {
				for _, m := range bucketModels(x) {
					if strings.EqualFold(m, model) {
						return true
					}
				}
			}
		}
	}
	return false
}
func (b balanceData) activePlanName() string {
	now := b.now()
	for _, p := range b.Plans {
		if livePlan(p, now) {
			if p.Name != "" {
				return p.Name
			}
			return "Gift"
		}
	}
	return ""
}
func teamHeaders(c credential, org, project string) http.Header {
	lang := getConfig().TeamLanguage
	return http.Header{"Bigmodel-Organization": {org}, "Bigmodel-Project": {project}, "Set-Language": {lang}, "Accept-Language": {"en-US,en"}}
}
func apiKeyFor(sender httpSender, c credential, auth, org, project string, team bool) (string, error) {
	endpoint := c.root() + "/api/biz/v1/organization/" + url.PathEscape(org) + "/projects/" + url.PathEscape(project) + "/api_keys"
	h := http.Header(nil)
	name := "zcode-api-key"
	if team {
		h = teamHeaders(c, org, project)
		name = "zcode-team-api-key"
	}
	b, err := apiCall(sender, "GET", endpoint, auth, "", h, nil)
	if err != nil {
		return "", err
	}
	var keys []struct {
		Name   string     `json:"name"`
		APIKey flexString `json:"apiKey"`
		Type   flexString `json:"keyType"`
	}
	if json.Unmarshal(b, &keys) != nil {
		return "", errors.New("invalid project key list")
	}
	id := ""
	for _, k := range keys {
		if k.Name == name && (!team || k.Type == "2") && k.APIKey != "" {
			id = string(k.APIKey)
		}
	}
	if id == "" {
		body := map[string]any{"name": name}
		if team {
			body["keyType"] = 2
		}
		b, err = apiCall(sender, "POST", endpoint, auth, "", h, body)
		if err != nil {
			return "", err
		}
		var out struct {
			APIKey flexString `json:"apiKey"`
		}
		if json.Unmarshal(b, &out) != nil || out.APIKey == "" {
			return "", errors.New("API key response has no identifier")
		}
		id = string(out.APIKey)
	}
	b, err = apiCall(sender, "GET", endpoint+"/copy/"+url.PathEscape(id), auth, "", h, nil)
	if err != nil {
		return "", err
	}
	var copy struct {
		Secret string `json:"secretKey"`
	}
	if json.Unmarshal(b, &copy) != nil || copy.Secret == "" {
		return "", errors.New("API key response has no secret")
	}
	return id + "." + copy.Secret, nil
}
func findPersonal(info customer) (string, string) {
	var org, project string
	for _, o := range info.Organizations {
		if o.ID == "" {
			continue
		}
		var picked string
		for _, p := range o.Projects {
			if p.ID == "" || p.Type == "2" {
				continue
			}
			if picked == "" || strings.Contains(p.Name, "默认项目") {
				picked = string(p.ID)
			}
			if strings.Contains(p.Name, "默认项目") {
				break
			}
		}
		if picked == "" {
			continue
		}
		if org == "" || strings.Contains(o.Name, "默认机构") {
			org = string(o.ID)
			project = picked
		}
		if strings.Contains(o.Name, "默认机构") {
			break
		}
	}
	return org, project
}
func signTeam(sender httpSender, c credential, biz string, info customer) (credential, string, error) {
	var why string
	var lastErr error
	for _, o := range info.Organizations {
		for _, p := range o.Projects {
			if o.ID == "" || p.ID == "" || p.Type != "2" {
				continue
			}
			org, proj := string(o.ID), string(p.ID)
			h := teamHeaders(c, org, proj)
			b, err := apiCall(sender, "GET", c.root()+"/api/biz/team/subscribe/product/querySubscribeDetail", biz, "", h, nil)
			if err != nil {
				lastErr = err
				continue
			}
			var d teamDetail
			if json.Unmarshal(b, &d) != nil {
				continue
			}
			status := strings.ToUpper(d.Status)
			grant := strings.ToUpper(d.Grant)
			if (d.Has == nil || *d.Has) && status == "EFFECTIVE" && grant == "VALID" {
				key, err := apiKeyFor(sender, c, biz, org, proj, true)
				if err != nil {
					return c, "", errors.New("the team's GLM Coding Plan: API key unavailable")
				}
				c.Key = key
				c.Org = org
				c.Project = proj
				c.Plan = d.Product
				if c.Plan == "" {
					c.Plan = "GLM Coding Team"
				}
				return c, "", nil
			}
			if why == "" && status == "EFFECTIVE" && grant == "UNASSIGNED" {
				why = fmt.Sprintf("this %s account is in a team with a GLM Coding Plan but has no seat on it yet — ask the team's admin to give it one, then sign in again", c.Site)
			}
			if why == "" && status == "EXPIRED" {
				why = fmt.Sprintf("the %s team's GLM Coding Plan this account is in has expired", c.Site)
			}
		}
	}
	if why == "" {
		if lastErr != nil {
			why = "team plan lookup failed: " + lastErr.Error()
		} else {
			why = "no team plan or assigned seat"
		}
	}
	return c, why, nil
}
func chooseCredential(sender httpSender, site, mode, siteToken, jwt, device, uid string) (credential, error) {
	c := credential{Site: site, JWT: jwt, Device: device, UID: uid}
	c.Base = c.base()
	biz := siteToken
	var lookupErr error
	if site == "zai" {
		b, err := apiCall(sender, "POST", "https://api.z.ai/api/auth/z/login", "", "", nil, map[string]string{"token": siteToken})
		if err == nil {
			var d struct {
				Access string `json:"access_token"`
			}
			if json.Unmarshal(b, &d) == nil && d.Access != "" {
				biz = "Bearer " + d.Access
			} else {
				biz = ""
				lookupErr = errors.New("Z.ai business login returned no access token")
			}
		} else {
			biz = ""
			lookupErr = fmt.Errorf("Z.ai business login failed: %w", err)
		}
	}
	var info customer
	var reason string
	if biz != "" {
		b, err := apiCall(sender, "GET", c.root()+"/api/biz/customer/getCustomerInfo", biz, "", nil, nil)
		if err == nil {
			if json.Unmarshal(b, &info) != nil {
				return c, errors.New("invalid customer information")
			}
			if mode != "team" {
				org, project := findPersonal(info)
				if org != "" {
					key, e := apiKeyFor(sender, c, biz, org, project, false)
					if e == nil {
						subsRaw, e := apiCall(sender, "GET", c.root()+"/api/biz/subscription/list", key, "", nil, nil)
						if e == nil {
							var subs []subscription
							if json.Unmarshal(subsRaw, &subs) != nil {
								return c, errors.New("invalid subscription response")
							}
							for _, sub := range subs {
								if strings.EqualFold(sub.Status, "VALID") {
									c.Key = key
									c.Plan = sub.Product
									return c, nil
								}
							}
						} else {
							lookupErr = fmt.Errorf("subscription lookup failed: %w", e)
						}
					} else {
						lookupErr = fmt.Errorf("project API key lookup failed: %w", e)
					}
				}
			}
			team, why, e := signTeam(sender, c, biz, info)
			if e != nil {
				return c, e
			}
			if team.Key != "" {
				return team, nil
			}
			reason = why
		} else {
			lookupErr = fmt.Errorf("customer lookup failed: %w", err)
		}
	}
	if mode == "team" {
		if reason == "" {
			if lookupErr != nil {
				reason = lookupErr.Error()
			} else {
				reason = "team sign-in unavailable"
			}
		}
		return c, errors.New(reason)
	}
	balance, err := fetchBalance(sender, c)
	if err == nil {
		if name := balance.activePlanName(); name != "" {
			c.Plan = name
			return c, nil
		}
	}
	if reason != "" && reason != "no team plan or assigned seat" {
		return c, errors.New(reason)
	}
	if err != nil && lookupErr != nil {
		return c, fmt.Errorf("%v; gift balance lookup failed: %w", lookupErr, err)
	}
	if lookupErr != nil {
		return c, lookupErr
	}
	if err != nil {
		return c, fmt.Errorf("gift balance lookup failed: %w", err)
	}
	return c, errors.New("no active GLM Coding Plan or ZCode gift plan")
}
