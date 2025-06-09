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

func parseEnvironmentString(envData string, config *EnvConfig) (map[string]string, error) {
    rawEnv := map[string]string{}
    
    if envData == "" {
        return rawEnv, nil
    }
    
    // Decode base64 encoded environment
    decoded, err := base64.StdEncoding.DecodeString(envData)
    if err != nil {
        return rawEnv, fmt.Errorf("failed to decode environment data: %v", err)
    }
    
    // Parse environment lines
    lines := strings.Split(string(decoded), "\n")
    for _, line := range lines {
        if line == "" {
            continue
        }
        
        pair := strings.SplitN(line, "=", 2)
        if len(pair) != 2 {
            continue
        }
        
        key := pair[0]
        value := pair[1]
        
        rawEnv[key] = value
    }
    
    // Apply configuration-based filtering
    return config.FilterEnvironment(rawEnv), nil
}

func getHostname() string {
    hostname, err := os.Hostname()
    if err != nil {
        return "unknown"
    }
    return hostname
}

func getLocalIP() string {
    addrs, err := net.InterfaceAddrs()
    if err != nil {
        return ""
    }
    for _, addr := range addrs {
        if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
            if ipnet.IP.To4() != nil {
                return ipnet.IP.String()
            }
        }
    }
    return ""
}

func main() {
    commandPtr := flag.String("command", "", "Command.")
    returnCodePtr := flag.String("return-code", "", "Return code.")
    startTimestampPtr := flag.String("start-timestamp", "", "Start timestamp.")
    endTimestampPtr := flag.String("end-timestamp", "", "End timestamp.")
    hostPtr := flag.String("host", "127.0.0.1", "Fluent-bit TCP host.")
    portPtr := flag.String("port", "5170", "Fluent-bit TCP port.")
    pwdPtr := flag.String("pwd", "", "Working directory (required).")
    envPtr := flag.String("env", "", "Base64 encoded environment variables.")
    configPtr := flag.String("env-config", "", "Path to environment filtering configuration file.")
    generateConfigPtr := flag.Bool("generate-config", false, "Generate default configuration file and exit.")
    testPtr := flag.Bool("test", false, "Test environment filtering and print results without sending data.")
    
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
    
    // Handle config generation
    if *generateConfigPtr {
        configPath := *configPtr
        if configPath == "" {
            homeDir, err := os.UserHomeDir()
            if err != nil {
                fmt.Println("error getting home directory:", err)
                return
            }
            configPath = filepath.Join(homeDir, ".totalrecall", "env-config.json")
        }
        
        if err := SaveDefaultConfig(configPath); err != nil {
            fmt.Println("error generating config:", err)
            return
        }
        
        fmt.Printf("Generated default configuration at: %s\n", configPath)
        fmt.Println("You can edit this file to customize environment variable filtering.")
        return
    }
    
    // Load environment filtering configuration
    envConfig, err := LoadEnvConfig(*configPtr)
    if err != nil {
        fmt.Println("error loading environment config:", err)
        return
    }
    
    // Handle test mode
    if *testPtr {
        fmt.Println("Testing environment filtering configuration...")
        fmt.Println("Environment variables that would be captured:")
        fmt.Println("")
        
        // Get current environment
        rawEnv := map[string]string{}
        for _, e := range os.Environ() {
            pair := strings.SplitN(e, "=", 2)
            if len(pair) != 2 {
                continue
            }
            rawEnv[pair[0]] = pair[1]
        }
        
        // Filter environment
        filtered := envConfig.FilterEnvironment(rawEnv)
        
        // Print results
        if len(filtered) == 0 {
            fmt.Println("(No environment variables would be captured)")
        } else {
            for key, value := range filtered {
                fmt.Printf("%s=%s\n", key, value)
            }
        }
        
        fmt.Printf("\nWould capture %d out of %d total environment variables\n", len(filtered), len(rawEnv))
        return
    }
    
    // Validate that PWD was provided
    if *pwdPtr == "" {
        fmt.Println("error: -pwd flag is required")
        return
    }
    
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
    event["pwd"] = *pwdPtr
    event["hostname"] = getHostname()
    
    // Add IP address if available
    if ip := getLocalIP(); ip != "" {
        event["ip_address"] = ip
    }
    
    // Parse and filter environment from parameter
    var env map[string]string
    if *envPtr != "" {
        env, err = parseEnvironmentString(*envPtr, envConfig)
        if err != nil {
            fmt.Println("error parsing environment:", err)
            return
        }
    } else {
        // Fallback to current environment if no env parameter provided
        rawEnv := map[string]string{}
        for _, e := range os.Environ() {
            pair := strings.SplitN(e, "=", 2)
            if len(pair) != 2 {
                continue
            }
            rawEnv[pair[0]] = pair[1]
        }
        env = envConfig.FilterEnvironment(rawEnv)
    }
    
    // Only include env in the event if it's not empty
    if len(env) > 0 {
        event["env"] = env
    }
    
    // Add config version for tracking
    event["_config_version"] = envConfig.Version
    
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
