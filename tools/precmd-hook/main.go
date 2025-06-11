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
    "encoding/json"
    "encoding/base64"
    "io/ioutil"
    "path/filepath"
)

// PreexecData matches the structure from precmd-hook
type PreexecData struct {
    Command        string    `json:"command"`
    Pwd            string    `json:"pwd"`
    StartTimestamp time.Time `json:"start_timestamp"`
    Environment    []string  `json:"environment"`
}

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

func parsePreexecData(encodedData string, config *EnvConfig) (*PreexecData, map[string]string, error) {
    if encodedData == "" {
        return nil, nil, fmt.Errorf("no preexec data provided")
    }
    
    // Decode base64
    decoded, err := base64.StdEncoding.DecodeString(encodedData)
    if err != nil {
        return nil, nil, fmt.Errorf("failed to decode preexec data: %v", err)
    }
    
    // Parse JSON
    var data PreexecData
    if err := json.Unmarshal(decoded, &data); err != nil {
        return nil, nil, fmt.Errorf("failed to parse preexec JSON: %v", err)
    }
    
    // Convert environment slice to map and apply filtering
    rawEnv := make(map[string]string)
    for _, envVar := range data.Environment {
        parts := strings.SplitN(envVar, "=", 2)
        if len(parts) == 2 {
            rawEnv[parts[0]] = parts[1]
        }
    }
    
    // Apply configuration-based filtering
    filteredEnv := config.FilterEnvironment(rawEnv)
    
    return &data, filteredEnv, nil
}

// Legacy function for backward compatibility
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

func sendViaUnixSocket(data []byte, socketPath string, timeout time.Duration) error {
	// Connect to unix domain socket
	conn, err := net.DialTimeout("unix", socketPath, timeout)
	if err != nil {
		return fmt.Errorf("failed to connect to proxy socket: %v", err)
	}
	defer conn.Close()

	// Set write deadline
	conn.SetWriteDeadline(time.Now().Add(timeout))
	
	// Send data
	_, err = conn.Write(append(data, '\n'))
	if err != nil {
		return fmt.Errorf("failed to send data: %v", err)
	}

	return nil
}

func sendDirectTLS(data []byte, host, port string, enableTLS bool, caFile, certFile, keyFile string, timeout time.Duration) error {
    address := fmt.Sprintf("%s:%s", host, port)
    
    if enableTLS {
        // ... existing TLS connection code ...
        caCert, err := ioutil.ReadFile(caFile)
        if err != nil {
            return fmt.Errorf("error loading CA certificate: %v", err)
        }
        caCertPool := x509.NewCertPool()
        caCertPool.AppendCertsFromPEM(caCert)
        
        cert, err := tls.LoadX509KeyPair(certFile, keyFile)
        if err != nil {
            return fmt.Errorf("error loading client certificate: %v", err)
        }
        
        tlsConfig := &tls.Config{
            RootCAs:            caCertPool,
            Certificates:       []tls.Certificate{cert},
            InsecureSkipVerify: false,
        }
        
        dialer := &net.Dialer{Timeout: timeout}
        conn, err := tls.DialWithDialer(dialer, "tcp", address, tlsConfig)
        if err != nil {
            return fmt.Errorf("error connecting with TLS: %v", err)
        }
        defer conn.Close()
        
        _, err = conn.Write(append(data, '\n'))
        return err
    } else {
        // ... existing non-TLS connection code ...
        conn, err := net.DialTimeout("tcp", address, timeout)
        if err != nil {
            return err
        }
        defer conn.Close()
        
        _, err = conn.Write(append(data, '\n'))
        return err
    }
}

func main() {
    // New flag for preexec data
    preexecDataPtr := flag.String("preexec-data", "", "Base64 encoded JSON from preexec-gather")
    
    // Legacy flags for backward compatibility
    returnCodePtr := flag.String("return-code", "", "Return code.")
    endTimestampPtr := flag.String("end-timestamp", "", "End timestamp (optional - will use current time if not provided).")
    hostPtr := flag.String("host", "127.0.0.1", "Fluent-bit TCP host.")
    portPtr := flag.String("port", "5170", "Fluent-bit TCP port.")
    configPtr := flag.String("env-config", "", "Path to environment filtering configuration file.")
    generateConfigPtr := flag.Bool("generate-config", false, "Generate default configuration file and exit.")
    testPtr := flag.Bool("test", false, "Test environment filtering and print results without sending data.")
    useSocketPtr := flag.Bool("use-socket", false, "Use unix domain socket proxy instead of direct TLS")
    socketPathPtr := flag.String("socket-path", "/tmp/totalrecall-proxy.sock", "Unix domain socket path")
     
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
    
    var event map[string]interface{}
    var env map[string]string
    
    // Parse preexec data
    preexecData, filteredEnv, err := parsePreexecData(*preexecDataPtr, envConfig)
    if err != nil {
        fmt.Println("error parsing preexec data:", err)
        return
    }
    
    env = filteredEnv
    event = make(map[string]interface{})
    event["command"] = strings.TrimSpace(preexecData.Command)
    event["start_timestamp"] = preexecData.StartTimestamp
    event["pwd"] = preexecData.Pwd
    
    // Common processing for both formats
    returnCode, err := strconv.Atoi(*returnCodePtr)
    if err != nil {
        fmt.Println("error:", err)
        return
    }
    event["return_code"] = returnCode
    if *endTimestampPtr != "" {
        // Backward compatibility: use provided timestamp
        event["end_timestamp"] = parseTimestamp(*endTimestampPtr)
    } else {
        // New behavior: generate timestamp in Go (eliminates gdate dependency)
        event["end_timestamp"] = time.Now()
    }
    event["hostname"] = getHostname()
    
    // Add IP address if available
    if ip := getLocalIP(); ip != "" {
        event["ip_address"] = ip
    }
    
    // Only include env in the event if it's not empty
    if len(env) > 0 {
        event["env"] = env
    }
    
    j, err := json.Marshal(event)
	if err != nil {
		fmt.Println("error:", err)
		return
	}

    // Choose connection method
    if *useSocketPtr {
        // Use unix domain socket proxy (fast path)
        if err := sendViaUnixSocket(j, *socketPathPtr, *timeoutPtr); err != nil {
            // Fallback to direct TLS if socket proxy is down
            fmt.Printf("Socket proxy failed, falling back to direct TLS: %v\n", err)
            sendDirectTLS(j, *hostPtr, *portPtr, *enableTLSPtr, *caFilePtr, *certFilePtr, *keyFilePtr, *timeoutPtr)
        }
    } else if *enableTLSPtr{
        // Use direct TLS connection (original behavior)
        sendDirectTLS(j, *hostPtr, *portPtr, *enableTLSPtr, *caFilePtr, *certFilePtr, *keyFilePtr, *timeoutPtr)
    } else {
        // Original non-TLS connection
        address := fmt.Sprintf("%s:%s", hostPtr, portPtr)
        conn, err := net.DialTimeout("tcp", address, *timeoutPtr)
        if err != nil {
            fmt.Println("error:", err)
            return
        }
        fmt.Fprintf(conn, string(j) + "\n")
        conn.Close()
    }
}
