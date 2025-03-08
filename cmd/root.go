package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"reflect"
	"strings"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "rjg",
	Short: "Generate JSON values based on the provided template.",
	Long:  `Generate structured JSON values using specified variables and a JSON template.`,
	Args:  cobra.MinimumNArgs(1), // JSON template must be needed
	PreRun: func(cmd *cobra.Command, args []string) {
		argsData.template = args[len(args)-1]
	},
	Run: func(cmd *cobra.Command, args []string) {
		file, err := os.Create("commands.jsonl")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error opening file: %s\n", err)
			os.Exit(1)
		}
		defer file.Close()

		var template interface{} // json template to be outputed
		err = json.Unmarshal([]byte(argsData.template), &template)
		if err != nil {
			fmt.Printf("Error: Invalid JSON template: %s\n", err)
			return
		}

		generator := newGenerator(argsData.variables)

		for i := 0; i < argsData.count; i++ {
			// Generate json data
			result, err := generator.Generate(i, template)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error during generating: %s\n", err)
				os.Exit(1)
			}

			// Encode json
			jsonOutput, err := json.Marshal(result)
			if err != nil {
				fmt.Println("JSON encode error:", err)
				os.Exit(1)
			}

			// Write to file
			_, err = file.WriteString(string(jsonOutput) + "\n")
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error writing to file: %s\n", err)
				os.Exit(1)
			}

			// Write to stdout
			fmt.Println(string(jsonOutput))
		}

	},
}

func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().IntVarP(&argsData.count, "count", "c", 1, "NUmber of JSON values to generate")
	rootCmd.PersistentFlags().StringToStringVarP(&argsData.variables, "var", "v", map[string]string{}, "Key-value pairs for variables")
}

type Args struct {
	count     int
	variables map[string]string
	template  string
}

var argsData Args

type Generator struct {
	prefix         string
	predefinedVars map[string]func() interface{}
	vars           map[string]interface{}
}

var prefixed = map[string]bool{
	"int":    true,
	"str":    true,
	"arr":    true,
	"obj":    true,
	"oneof":  true,
	"option": true,
	"i":      true,
	"u8":     true,
	"u16":    true,
	"u32":    true,
	"i8":     true,
	"i16":    true,
	"i32":    true,
	"i64":    true,
	"digit":  true,
	"bool":   true,
	"alpha":  true,
}

func isPredefinedVar(value string) bool {
	return prefixed[value]
}

func newGenerator(userVars map[string]string) Generator {
	g := Generator{
		prefix:         "$",
		predefinedVars: nil,
		vars:           make(map[string]interface{}),
	}
	for k, v := range userVars {
		var parsedValue interface{}
		err := json.Unmarshal([]byte(v), &parsedValue)
		if err != nil {
			fmt.Printf("WARNING: Failed to parse user variable %q, storing as string: %s\n", k, err)
			parsedValue = v
		}
		g.vars[k] = parsedValue
	}

	return g
}

func (g Generator) Generate(i int, template interface{}) (interface{}, error) {
	switch t := template.(type) {
	case map[string]interface{}:
		generated := make(map[string]interface{})
		for key, val := range t {
			// handle generator and return generated value
			if isPredefinedVar(strings.TrimPrefix(key, g.prefix)) {
				result, err := g.resolveVar(g.prefix, key, val, i)
				if err != nil {
					return nil, fmt.Errorf("failed to resolve generator %q: %w", key, err)
				}
				return result, nil
			}

			// handle template_json
			resolvedKey, err := g.Generate(i, key)
			if err != nil {
				return nil, fmt.Errorf("failed to resolve key %q: %w", key, err)
			}
			resolvedVal, err := g.Generate(i, val)
			if err != nil {
				return nil, fmt.Errorf("failed to resolve val %q: %w", val, err)
			}

			if strKey, ok := resolvedKey.(string); ok {
				generated[strKey] = resolvedVal
			} else {
				return nil, errors.New("keys must resolve to strings")
			}
		}
		return generated, nil

	case string:
		generated, err := g.resolveVar(g.prefix, t, nil, i)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve variable %q: %w", t, err)
		}
		return generated, nil
	default:
		return template, nil
	}
}

func (g Generator) resolveVar(prefix string, variable string, params interface{}, i int) (interface{}, error) {
	trimmedVar := strings.TrimPrefix(variable, prefix)
	switch trimmedVar {
	case "int":
		if paramsMap, ok := params.(map[string]interface{}); ok {
			min, minOk := convertToInt(paramsMap["min"])
			max, maxOk := convertToInt(paramsMap["max"])
			if !minOk || !maxOk {
				return nil, errors.New("invalid min or max value for $int")
			}
			return rand.IntN(max-min+1) + min, nil
		}
		return nil, errors.New("$int requires a {min, max} object")
	case "str":
		if paramsList, ok := params.([]interface{}); ok {
			var strBuilder strings.Builder
			for _, elem := range paramsList {
				resolved, err := g.Generate(i, elem)
				if err != nil {
					return nil, err
				}
				strBuilder.WriteString(fmt.Sprintf("%v", resolved))
			}
			return strBuilder.String(), nil
		}

		// In case of params is not an array, just a object
		result, err := g.Generate(i, params)
		if err != nil {
			return nil, err
		}
		resultStr, err := joinAnySlice(result)
		if err != nil {
			return nil, err
		}
		return resultStr, nil

	case "arr":
		if paramsMap, ok := params.(map[string]interface{}); ok {
			resolvedLen, err := g.Generate(i, paramsMap["len"])
			if err != nil {
				return nil, fmt.Errorf("failed to resolve length for $arr: %w", err)
			}

			length, ok := convertToInt(resolvedLen)
			if !ok {
				return nil, errors.New("invalid len value for $arr")
			}

			val, valExists := paramsMap["val"]
			if !valExists {
				return nil, errors.New("missing val for $arr")
			}

			arr := make([]interface{}, length)
			for z := 0; z < length; z++ {
				resolvedVal, err := g.Generate(i, val)
				if err != nil {
					return nil, err
				}
				arr[z] = resolvedVal
			}
			return arr, nil

		}
		return nil, errors.New("$arr requires a {len, val} object")
	case "obj":
		if paramsList, ok := params.([]interface{}); ok && len(paramsList) > 0 {

			randomIndex := rand.IntN(len(paramsList))
			selectedObj, ok := paramsList[randomIndex].(map[string]interface{})
			if !ok {
				return nil, errors.New("$obj must contain a list of objects")
			}
			result, err := g.Generate(i, selectedObj)
			if err != nil {
				return nil, err
			}
			return result, nil

		}
		return nil, errors.New("$obj requires objects")
	case "oneof":
		if paramsList, ok := params.([]interface{}); ok {
			randomIndex := rand.IntN(len(paramsList))
			oneof := paramsList[randomIndex]
			resolved, err := g.Generate(i, oneof)
			if err != nil {
				return nil, err
			}
			return resolved, nil
		}
		return nil, errors.New("$oneof requires a list of values")
	case "option":
		if params == nil {
			return nil, errors.New("$option requires a valid parameter")
		}

		oneofParams := []interface{}{params}
		result, err := g.Generate(i, map[string]interface{}{"$oneof": oneofParams})
		if err != nil {
			return nil, err
		}
		return result, nil

	case "i":
		return i, nil // return iteration value
	case "u8":
		return uint8(rand.UintN(256)), nil
	case "u16":
		return uint16(rand.UintN(65536)), nil
	case "u32":
		return rand.Uint32(), nil
	case "i8":
		return int8(rand.IntN(256) - 128), nil
	case "i16":
		return int16(rand.IntN(65536) - 32768), nil
	case "i32":
		return rand.Int32(), nil
	case "i64":
		return rand.Int64(), nil
	case "digit":
		return rand.IntN(10), nil
	case "bool":
		return rand.IntN(2) == 1, nil
	case "alpha":
		if rand.IntN(2) == 0 {
			return string(rune('a' + rand.IntN(26))), nil
		}
		return string(rune('A' + rand.IntN(26))), nil
	default:
		if strings.HasPrefix(variable, prefix) {
			// handle user-defined variables
			if userdefinedVar, isExist := g.vars[strings.TrimPrefix(variable, prefix)]; isExist {
				result, err := g.Generate(i, userdefinedVar)
				if err != nil {
					return nil, fmt.Errorf("failed to resolve variable %q: %w", variable, err)
				}
				return result, nil
			}
			return nil, fmt.Errorf("undefined variable: %q", variable)
		}
		return variable, nil
	}
}

func convertToInt(value interface{}) (int, bool) {
	switch v := value.(type) {
	case float64:
		return int(v), true
	case int:
		return v, true
	default:
		return 0, false
	}
}

func joinAnySlice(result interface{}) (string, error) {
	v := reflect.ValueOf(result)

	if v.Kind() != reflect.Slice {
		return "", fmt.Errorf("expected slice but got %T", result)
	}

	var strSlice []string
	for i := 0; i < v.Len(); i++ {
		elem := v.Index(i)
		strSlice = append(strSlice, fmt.Sprint(elem.Interface()))
	}

	return strings.Join(strSlice, ""), nil
}
