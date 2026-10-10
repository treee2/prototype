package main

import (
	"fmt"
	"math"
)

// Rule — правило вида ЕСЛИ <условия> ТО <диагноз>.
type Rule struct {
	ID         string            `json:"id"`
	If         map[string]string `json:"if"`
	Then       string            `json:"fault"`
	Support    int               `json:"support"`
	Confidence float64           `json:"confidence"`
}

// RuleSet — выходной файл с правилами.
type RuleSet struct {
	Source string `json:"source"`
	Count  int    `json:"count"`
	Rules  []Rule `json:"rules"`
}

type group struct {
	cond   map[string]string
	counts map[string]int
	total  int
}

// buildRules группирует наблюдения по условиям и формирует правило для каждой группы,
// если диагноз подтверждён не меньше minSupport раз и его доля не меньше minConf.
func buildRules(recs []Record, minSupport int, minConf float64) []Rule {
	groups := make(map[string]*group)
	for _, r := range recs {
		k := r.key()
		g, ok := groups[k]
		if !ok {
			g = &group{cond: r.Features, counts: make(map[string]int)}
			groups[k] = g
		}
		g.counts[r.Fault]++
		g.total++
	}

	var rules []Rule
	for _, k := range sortedKeys(groups) {
		g := groups[k]

		best, bestN := "", 0
		for _, label := range sortedKeys(g.counts) {
			if n := g.counts[label]; n > bestN {
				best, bestN = label, n
			}
		}

		conf := float64(bestN) / float64(g.total)
		if bestN < minSupport || conf < minConf {
			continue
		}

		cond := make(map[string]string, len(g.cond))
		for f, v := range g.cond {
			cond[f] = v
		}
		rules = append(rules, Rule{
			ID:         fmt.Sprintf("R%03d", len(rules)+1),
			If:         cond,
			Then:       best,
			Support:    bestN,
			Confidence: math.Round(conf*1000) / 1000,
		})
	}
	return rules
}
