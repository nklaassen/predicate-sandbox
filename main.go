package main

import (
	"fmt"
	"regexp"

	"github.com/gravitational/trace"
	"github.com/vulcand/predicate"
)

func check(err error) {
	if err != nil {
		panic(err)
	}
}

type unaryStringPredicate func(string) (bool, error)

func matches(expr string) (unaryStringPredicate, error) {
	return func(s string) (bool, error) {
		matched, err := regexp.MatchString(expr, s)
		if err != nil {
			return false, trace.Wrap(err)
		}
		return matched, nil
	}, nil
}

func filter(ss []string, pred unaryStringPredicate) ([]string, error) {
	var out []string

	for _, s := range ss {
		matched, err := pred(s)
		if err != nil {
			return nil, trace.Wrap(err)
		}
		if matched {
			out = append(out, s)
		}
	}

	return out, nil
}

type stringTransform func(string) (string, error)

func transform(ss []string, transform stringTransform) ([]string, error) {
	out := make([]string, len(ss))
	for i, s := range ss {
		s, err := transform(s)
		if err != nil {
			return nil, trace.Wrap(err)
		}
		out[i] = s
	}
	return out, nil
}

func replace(expr, replacement string) (stringTransform, error) {
	re, err := regexp.Compile(expr)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	return func(input string) (string, error) {
		if !re.MatchString(input) {
			return "", nil
		}
		return re.ReplaceAllString(input, replacement), nil
	}, nil
}

func ifelse(pred predicate.BoolPredicate, a, b interface{}) interface{} {
	if pred() {
		return a
	}
	return b
}

func list(args ...interface{}) ([]string, error) {
	var out []string
	for _, arg := range args {
		switch a := arg.(type) {
		case string:
			out = append(out, a)
		case []string:
			out = append(out, a...)
		default:
			return nil, trace.BadParameter("args to list must be string or []string, got %T", arg)
		}
	}
	return out, nil
}

type stringMatcher string

func (s stringMatcher) matches(value interface{}) bool {
	if valueStr, ok := value.(string); ok {
		return valueStr == string(s)
	}
	return false
}

type defaultMatcher struct{}

func (_ defaultMatcher) matches(_ interface{}) bool {
	return true
}

type matcher interface {
	matches(interface{}) bool
}

type option struct {
	matcher matcher
	value   interface{}
}

func newOption(m interface{}, value interface{}) (option, error) {
	switch m_type := m.(type) {
	case string:
		return option{stringMatcher(m_type), value}, nil
	}
	return option{}, trace.BadParameter("unsupported matcher type %T", m)
}

func newDefaultOption(value interface{}) option {
	return option{defaultMatcher{}, value}
}

func match(input interface{}, options ...option) (interface{}, error) {
	for _, opt := range options {
		if opt.matcher.matches(input) {
			return opt.value, nil
		}
	}
	return nil, trace.NotFound("no matching option")
}

func newParser(traits map[string][]string) predicate.Parser {
	parser, err := predicate.NewParser(predicate.Def{
		Operators: predicate.Operators{
			AND: predicate.And,
			OR:  predicate.Or,
			NOT: predicate.Not,
		},
		Functions: map[string]interface{}{
			"equals":         predicate.Equals,
			"contains":       predicate.Contains,
			"matches":        matches,
			"filter":         filter,
			"transform":      transform,
			"replace":        replace,
			"ifelse":         ifelse,
			"list":           list,
			"option":         newOption,
			"match":          match,
			"default_option": newDefaultOption,
		},
		GetIdentifier: func(fields []string) (interface{}, error) {
			switch len(fields) {
			case 1:
				if fields[0] != "external" {
					return nil, trace.NotFound("identifier %q not found", fields[0])
				}
				return traits, nil
			case 2:
				if fields[0] != "external" {
					return nil, trace.NotFound("identifier %q not found", fields[0])
				}
				return traits[fields[1]], nil
			default:
				return nil, trace.BadParameter("unsupported fields length: %v", fields)
			}
		},
		GetProperty: predicate.GetStringMapValue,
	})
	check(err)
	return parser
}

func eval(p predicate.Parser, expr string) {
	fmt.Println("evaluating:", expr)
	result, err := p.Parse(expr)
	check(err)

	switch r := result.(type) {
	case predicate.BoolPredicate:
		fmt.Println("result:", r())
	default:
		fmt.Printf("result: %+v\n", r)
	}
	fmt.Println()
}

func main() {
	traits := map[string][]string{
		"username": {"my-username"},
		"email":    {"nic@goteleport.com"},
		"groups":   {"env-staging", "env-qa", "devs"},
	}

	parser := newParser(traits)

	eval(parser, `external.groups`)
	eval(parser, `filter(external.groups, matches("env"))`)
	eval(parser, `ifelse(contains(external.groups, "contractors"), "first", "second")`)
	eval(parser, `transform(external.username, replace("-", "_"))`)

	eval(parser, `
list(
	"ubuntu",
	transform(external.username, replace("-", "_")),
	ifelse(contains(external.email, "nic@goteleport.com"), "root", list()),
	transform(filter(external.email, matches("@goteleport.com")), replace("^(.*)@goteleport.com", "$1")),
)
`)

	eval(parser, `
list(
	transform(
		filter(external.groups, matches("^env-\\w+$")),
		replace("^env-(\\w+)$", "$1")),
	ifelse(
		contains(external.groups, "contractors"),
		list(),
		ifelse(contains(external.groups, "devs"), "dev", list())))
`)

	eval(parser, `
list(
	ifelse(
		contains(external.groups, "devs"),
		list("dev", "staging"),
		list()),
	ifelse(
		contains(external.groups, "qa"),
		"qa",
		list()),
)
`)

	eval(parser, `list(list("a", "b"), "c")`)
	eval(parser, `option("a", list("b", "c"))`)
	eval(parser, `
match("asdf",
  option("asdf", list("a", "b", "c")),
  option("a", list("b", "c")))`)
	eval(parser, `
match("asdfzxcv",
  option("asdf", list("a", "b", "c")),
  option("a", list("b", "c")),
  option("asdfzxcv", list(external.groups, "b", "c")),
  default_option(list("default")),
  )`)
}
