package main
import (
    "flag"
    "fmt"
    "time"
    "os"
    "net"
    "strings"
    "strconv"
    "regexp"
    "crypto/sha256"
    "encoding/json"
    "encoding/base64"
)


func parseTimestamp(t string) time.Time {
    startRune := []rune(t)
    startRune[10] = 'T'
	ts, err := time.Parse(time.RFC3339Nano, string(startRune))
	if err != nil {
		fmt.Println("error:", err)
		panic(err)
	}
    return ts
}

func getMaskedEnvVar(key string, value string) string {
    patterns := []string{"secret", "password", "key"}
	for _, pattern := range patterns {
		matched, err := regexp.MatchString("(?i)" + pattern, key)
		if err != nil {
			fmt.Println("error:", err)
			panic(err)
		}
		if matched {
            return fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(value)))
		}
	}
	return value
}

func shouldSkipEnvVar(key string, value string) bool {
    patterns := []string{"^_+", "^PS1$", "^TERM$", "TOTALRECALLROOT"}
	for _, pattern := range patterns {
		matched, err := regexp.MatchString("(?i)" + pattern, key)
		if err != nil {
			fmt.Println("error:", err)
			panic(err)
		}
		if matched {
            return matched
		}
	}
	return false
}



func main() {
    commandPtr := flag.String("command", "", "Command.")
    returnCodePtr := flag.String("return-code", "", "Return code.")
    startTimestampPtr := flag.String("start-timestamp", "", "Start timestamp.")
    endTimestampPtr := flag.String("end-timestamp", "", "End timestamp.")
    flag.Parse()

    event := make(map[string]interface{})
	data, err := base64.StdEncoding.DecodeString(*commandPtr)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
    event["command"] = strings.TrimSpace(string(data))

    returnCode, err := strconv.Atoi(*returnCodePtr)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
    event["return_code"] = returnCode
    event["start_timestamp"] = parseTimestamp(*startTimestampPtr)
    event["end_timestamp"] = parseTimestamp(*endTimestampPtr)

    env := map[string]string{}
    for _, e := range os.Environ() {
        pair := strings.SplitN(e, "=", 2)
        key := string(pair[0])
        value := string(pair[1])
        if shouldSkipEnvVar(key, value) { 
            continue
        }
        env[key] = getMaskedEnvVar(key, value)
       }
    event["env"] = env

    j, err := json.Marshal(event)
	if err != nil {
		fmt.Println("error:", err)
		return
	}

    timeout, err := time.ParseDuration("50ms") 
	if err != nil {
		fmt.Println("error:", err)
		return
	}
    conn, _ := net.DialTimeout("tcp", "127.0.0.1:5170", timeout)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Fprintf(conn, string(j) + "\n")
    conn.Close()

}
