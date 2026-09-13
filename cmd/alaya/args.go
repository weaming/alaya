package main

import (
	"flag"
	"strings"
)

// parseArgs 解析标志并返回位置参数，允许两者以任意顺序混写。
//
// Go 的 flag 包遇到第一个位置参数就停止解析，于是 `search 关键词 --scope X`
// 里的 --scope 会被当成位置参数——用户自然会这么写，报错却毫无提示。
// 标准库不解决这个问题，只能自己先分拣。
//
// 代价是无法区分「值为负数的标志」与「短横线开头的位置参数」；
// 本仓库的标志都取字符串或正整数，不受影响。
func parseArgs(flags *flag.FlagSet, args []string) []string {
	needsValue := make(map[string]bool)
	flags.VisitAll(func(f *flag.Flag) {
		needsValue["-"+f.Name] = true
		needsValue["--"+f.Name] = true
	})

	var (
		flagArgs   []string
		positional []string
	)

	for i := 0; i < len(args); i++ {
		arg := args[i]

		if !strings.HasPrefix(arg, "-") {
			positional = append(positional, arg)
			continue
		}

		flagArgs = append(flagArgs, arg)

		// --name=value 自带取值，不占用下一个参数
		if strings.Contains(arg, "=") {
			continue
		}

		name := arg
		if needsValue[name] && i+1 < len(args) {
			i++
			flagArgs = append(flagArgs, args[i])
		}
	}

	flags.Parse(flagArgs)

	return positional
}
