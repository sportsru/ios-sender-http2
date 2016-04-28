package main

import "fmt"

type ErrorWithContext struct {
	Msg string
	Ctx []interface{}
}

func (e ErrorWithContext) Error() string {
	res := e.Msg
	i := 0
	var norm bool
	for {
		if i == len(e.Ctx) {
			break
		}
		var value, key interface{}
		key = e.Ctx[i]
		i++
		if i != len(e.Ctx) {
			value = e.Ctx[1]
			i++
		} else {
			value = nil
			norm = true
		}
		res += fmt.Sprintf(" %v=%v", key, value)
	}
	if norm {
		res += " ErrorWithContext_ERROR=\"Normalized odd number of arguments by adding nil\""
	}

	return res
}
