package researchmemory

import (
	contract "lead-gen/contracts/lead-management/researchmemory"
	"sort"
	"time"
)

// IdentityNode сохраняет исходный субъект; сильные ключи включают тип и реестр.
type IdentityNode struct {
	ID                 string
	CreatedAt          time.Time
	Identifiers        map[string]string
	LegalJurisdictions []string
}
type IdentityProjection struct {
	Canonical map[string]string
	States    map[string]string
}

// ProjectIdentity строит только явно принятые связи. Наличие похожего имени,
// домена или пути в предметном графе не добавляет ребро подтверждения.
func ProjectIdentity(nodes []IdentityNode, decisions []contract.IdentityDecision, available map[string]bool) IdentityProjection {
	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].CreatedAt.Equal(nodes[j].CreatedAt) {
			return nodes[i].ID < nodes[j].ID
		}
		return nodes[i].CreatedAt.Before(nodes[j].CreatedAt)
	})
	rank := map[string]int{}
	parent := map[string]string{}
	members := map[string][]string{}
	keys := map[string]map[string]string{}
	jurisdictions := map[string][]string{}
	for i, n := range nodes {
		rank[n.ID] = i
		parent[n.ID] = n.ID
		members[n.ID] = []string{n.ID}
		keys[n.ID] = n.Identifiers
		jurisdictions[n.ID] = n.LegalJurisdictions
	}
	var root func(string) string
	root = func(id string) string {
		p, ok := parent[id]
		if !ok {
			return ""
		}
		if p != id {
			parent[id] = root(p)
		}
		return parent[id]
	}
	revoked := map[string]bool{}
	for _, d := range decisions {
		if d.Action == "revoke" {
			revoked[d.RevokeDecisionID] = true
		}
	}
	states := map[string]string{}
	rejects := [][2]string{}
	for _, d := range decisions {
		if revoked[d.ID] {
			states[d.ID] = "revoked"
			continue
		}
		if d.Action == "revoke" {
			states[d.ID] = "active"
			continue
		}
		if d.Action == "reject" && !available[d.ID] {
			states[d.ID] = "unavailable"
			continue
		}
		if d.Action == "reject" {
			states[d.ID] = "active"
			rejects = append(rejects, [2]string{d.LeftEntityID, d.RightEntityID})
			continue
		}
		if !available[d.ID] {
			states[d.ID] = "unavailable"
		}
	}
	sort.Slice(decisions, func(i, j int) bool {
		if decisions[i].RecordedAt.Equal(decisions[j].RecordedAt) {
			return decisions[i].ID < decisions[j].ID
		}
		return decisions[i].RecordedAt.Before(decisions[j].RecordedAt)
	})
	for _, d := range decisions {
		if d.Action != "link" || revoked[d.ID] || !available[d.ID] {
			continue
		}
		left, right := root(d.LeftEntityID), root(d.RightEntityID)
		if left == "" || right == "" {
			states[d.ID] = "conflict"
			continue
		}
		if left == right {
			states[d.ID] = "active"
			continue
		}
		incompatible := false
		for _, pair := range rejects {
			a, b := root(pair[0]), root(pair[1])
			if (a == left && b == right) || (a == right && b == left) {
				incompatible = true
			}
		}
		unionKeys := map[string]string{}
		legalScopes := map[string]bool{}
		for _, group := range []string{left, right} {
			for _, id := range members[group] {
				for _, scope := range jurisdictions[id] {
					legalScopes[scope] = true
				}
				for namespace, value := range keys[id] {
					if old, ok := unionKeys[namespace]; ok && old != value {
						incompatible = true
					}
					unionKeys[namespace] = value
				}
			}
		}
		if len(legalScopes) > 1 {
			incompatible = true
		}
		if incompatible {
			states[d.ID] = "conflict"
			continue
		}
		if rank[left] > rank[right] {
			left, right = right, left
		}
		parent[right] = left
		members[left] = append(members[left], members[right]...)
		states[d.ID] = "active"
	}
	canonical := map[string]string{}
	for _, n := range nodes {
		canonical[n.ID] = root(n.ID)
	}
	return IdentityProjection{Canonical: canonical, States: states}
}
