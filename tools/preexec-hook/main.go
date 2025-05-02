package main
import (
    "crypto/tls"
    "crypto/x509"
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
    "io/ioutil"
)
func parseTimestamp(t string) time.Time {
    if t == "" {
        return time.Now()
    }
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
    hostPtr := flag.String("host", "127.0.0.1", "Fluent-bit TCP host.")
    portPtr := flag.String("port", "5170", "Fluent-bit TCP port.")
    timeout, err := time.ParseDuration("3s")
    if err != nil {
	   fmt.Println("error:", err)
	   return
    }
    timeoutPtr := flag.Duration("timeout", timeout, "Fluent bit connection timeout.")
    
    // Add TLS certificate flags
    enableTLSPtr := flag.Bool("tls", false, "Enable TLS connection")
    caFilePtr := flag.String("tls-ca-file", "certs/ca.crt", "CA certificate file")
    certFilePtr := flag.String("tls-cert-file", "certs/client.crt", "Client certificate file")
    keyFilePtr := flag.String("tls-key-file", "certs/client.key", "Client private key file")
    
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
    
    // Connection handling with TLS support
    address := fmt.Sprintf("%s:%s", *hostPtr, *portPtr)
    
    if *enableTLSPtr {
        // Load CA cert
        caCert, err := ioutil.ReadFile(*caFilePtr)
        if err != nil {
            fmt.Println("error loading CA certificate:", err)
            return
        }
        caCertPool := x509.NewCertPool()
        caCertPool.AppendCertsFromPEM(caCert)
        
        // Load client cert and key
        cert, err := tls.LoadX509KeyPair(*certFilePtr, *keyFilePtr)
        if err != nil {
            fmt.Println("error loading client certificate:", err)
            return
        }
        
        // Create TLS config
        tlsConfig := &tls.Config{
            RootCAs:            caCertPool,
            Certificates:       []tls.Certificate{cert},
            InsecureSkipVerify: false,
        }
        
        // Establish TLS connection with timeout
        dialer := &net.Dialer{Timeout: *timeoutPtr}
        conn, err := tls.DialWithDialer(dialer, "tcp", address, tlsConfig)
        if err != nil {
            fmt.Println("error connecting with TLS:", err)
            return
        }
        
        fmt.Fprintf(conn, string(j) + "\n")
        conn.Close()
    } else {
        // Original non-TLS connection
        conn, err := net.DialTimeout("tcp", address, *timeoutPtr)
        if err != nil {
            fmt.Println("error:", err)
            return
        }
        fmt.Fprintf(conn, string(j) + "\n")
        conn.Close()
    }
}
