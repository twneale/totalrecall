package main

import (
    "crypto/sha256"
    "encoding/json"
    "fmt"
    "io/ioutil"
    "os"
    "path/filepath"
    "regexp"
)

// EnvConfig holds the configuration for environment variable filtering
// 
// The filtering logic works as follows:
// 1. Absolute deny patterns are checked first (these vars are never included)
// 2. Variables must match the allowlist (exact or pattern) to be considered
// 3. Allowed variables that match denylist patterns are HASHED but INCLUDED
// 4. Other allowed variables are included in plaintext
//
// This design ensures sensitive variables provide context without exposing secrets.
type EnvConfig struct {
    // High-value environment variables that provide context for command prediction
    Allowlist struct {
        // Exact matches (case-sensitive)
        Exact []string `json:"exact"`
        // Regex patterns for matching variable names
        Patterns []string `json:"patterns"`
    } `json:"allowlist"`
    
    // Sensitive or low-value variables (different handling)
    Denylist struct {
        // Exact matches that should NEVER be included (case-sensitive)
        Exact []string `json:"exact"`
        // Regex patterns for variables that should be HASHED but INCLUDED (case-insensitive)  
        Patterns []string `json:"patterns"`
    } `json:"denylist"`
    
    // Whether to hash allowed variables that match sensitive patterns
    // When true, variables matching denylist patterns will be hashed but still included
    // When false, sensitive variables are included in plaintext (not recommended)
    HashSensitiveValues bool `json:"hash_sensitive_values"`
}

// DefaultEnvConfig returns a sensible default configuration
func DefaultEnvConfig() *EnvConfig {
    return &EnvConfig{
        Allowlist: struct {
            Exact    []string `json:"exact"`
            Patterns []string `json:"patterns"`
        }{
            // High-value exact matches
            Exact: []string{
                "PWD",           // Current directory (critical for context)
                "OLDPWD",        // Previous directory
                "USER",          // Current user
                "HOME",          // Home directory
                "SHELL",         // Current shell
                "LANG",          // Locale (affects command behavior)
                "LC_ALL",        // Locale override
                "TZ",            // Timezone
                "EDITOR",        // Default editor
                "PAGER",         // Default pager
                "BROWSER",       // Default browser
                "TMPDIR",        // Temporary directory
                "XDG_CONFIG_HOME", // Config directory
                "XDG_DATA_HOME",   // Data directory
                "XDG_CACHE_HOME",  // Cache directory
                
                // Common development environment indicators
                "NODE_ENV",      // Node.js environment
                "RAILS_ENV",     // Rails environment
                "DJANGO_SETTINGS_MODULE", // Django settings
                "FLASK_ENV",     // Flask environment
                "ENVIRONMENT",   // Generic environment indicator
                "ENV",           // Generic environment indicator
                "STAGE",         // Deployment stage
                "DEPLOY_ENV",    // Deployment environment
                
                // Version managers
                "RBENV_VERSION", // Ruby version
                "PYENV_VERSION", // Python version
                "NVM_CURRENT",   // Node version
                "JAVA_HOME",     // Java installation
                "GOPATH",        // Go workspace
                "GOROOT",        // Go installation
                "CARGO_HOME",    // Rust cargo home
                "RUSTUP_HOME",   // Rust installation
                
                // Cloud provider indicators
                "AWS_PROFILE",   // AWS profile
                "AWS_REGION",    // AWS region
                "GOOGLE_CLOUD_PROJECT", // GCP project
                "AZURE_RESOURCE_GROUP", // Azure resource group
                
                // Container/orchestration
                "DOCKER_HOST",   // Docker daemon
                "KUBERNETES_NAMESPACE", // K8s namespace
                "KUBECTL_CONTEXT", // kubectl context
                
                // CI/CD indicators
                "CI",            // CI environment flag
                "GITHUB_ACTIONS", // GitHub Actions
                "JENKINS_URL",   // Jenkins
                "GITLAB_CI",     // GitLab CI
                "TRAVIS",        // Travis CI
                "CIRCLECI",      // CircleCI
            },
            
            // High-value patterns (case-sensitive regex)
            Patterns: []string{
                `^[A-Z_]+_ENV$`,        // *_ENV variables
                `^[A-Z_]+_ENVIRONMENT$`, // *_ENVIRONMENT variables
                `^[A-Z_]+_STAGE$`,      // *_STAGE variables
                `^[A-Z_]+_PROFILE$`,    // *_PROFILE variables
                `^[A-Z_]+_NAMESPACE$`,  // *_NAMESPACE variables
                `^[A-Z_]+_CLUSTER$`,    // *_CLUSTER variables
                `^[A-Z_]+_REGION$`,     // *_REGION variables
                `^[A-Z_]+_ZONE$`,       // *_ZONE variables
                `^[A-Z_]+_BRANCH$`,     // *_BRANCH variables (git branch indicators)
                `^[A-Z_]+_VERSION$`,    // *_VERSION variables
                `^[A-Z_]+_PATH$`,       // *_PATH variables (tool paths)
                `^[A-Z_]+_HOME$`,       // *_HOME variables (tool homes)
                `^[A-Z_]+_ROOT$`,       // *_ROOT variables (project roots)
                `^[A-Z_]+_CONFIG$`,     // *_CONFIG variables
                `^[A-Z_]+_URL$`,        // *_URL variables (service URLs)
                `^[A-Z_]+_HOST$`,       // *_HOST variables (service hosts)
                `^[A-Z_]+_PORT$`,       // *_PORT variables (service ports)
                `^[A-Z_]+_KEY$`,        // *_KEY variables (API keys, etc.)
                `^GIT_`,                // Git-related variables
                `^DOCKER_`,             // Docker-related variables
                `^K8S_`,                // Kubernetes-related variables
                `^KUBE_`,               // Kubernetes-related variables
                `^HELM_`,               // Helm-related variables
                `^TERRAFORM_`,          // Terraform-related variables
            },
        },
        
        Denylist: struct {
            Exact    []string `json:"exact"`
            Patterns []string `json:"patterns"`
        }{
            // Always exclude these exact matches (never include, even hashed)
            Exact: []string{
                "_",             // Last argument of previous command
                "PS1", "PS2", "PS3", "PS4", // Prompt strings
                "TERM",          // Terminal type
                "LINES", "COLUMNS", // Terminal dimensions
                "HISTFILE", "HISTSIZE", "HISTCONTROL", "HISTTIMEFORMAT", // History settings
                "IFS",           // Internal field separator
                "OPTIND", "OPTARG", "OPTERR", // getopt variables
                "RANDOM",        // Random number
                "SECONDS",       // Seconds since shell start
                "BASH_VERSINFO", "BASH_VERSION", // Shell version info
                "BASH_ARGC", "BASH_ARGV", "BASH_ARGV0", // Bash internals
                "BASH_COMMAND", "BASH_EXECUTION_STRING", // Bash internals
                "BASH_LINENO", "BASH_SOURCE", "BASH_SUBSHELL", // Bash internals
                "FUNCNAME", "BASH_REMATCH", // Bash internals
                "PIPESTATUS",    // Pipeline status
                "REPLY",         // read builtin variable
                "SHELLOPTS", "BASHOPTS", // Shell options
                "TOTALRECALLROOT", // Our own variable
            },
            
            // Sensitive patterns that should be HASHED but INCLUDED (not excluded)
            // These variables have value for context but contain sensitive data
            Patterns: []string{
                `(?i)password`,     // Any variable with "password"
                `(?i)secret`,       // Any variable with "secret"  
                `(?i)key`,          // Any variable with "key" (API keys, etc.)
                `(?i)token`,        // Any variable with "token"
                `(?i)auth`,         // Any variable with "auth"
                `(?i)credential`,   // Any variable with "credential"
                `(?i)private`,      // Any variable with "private"
                `(?i)session`,      // Any variable with "session"
                `(?i)cookie`,       // Any variable with "cookie"
                `(?i)cert`,         // Any variable with "cert"
                `(?i)ssl`,          // Any variable with "ssl"
                `(?i)tls`,          // Any variable with "tls"
                `(?i)oauth`,        // Any variable with "oauth"
                `(?i)jwt`,          // Any variable with "jwt"
                `(?i)bearer`,       // Any variable with "bearer"
                `(?i)access`,       // Any variable with "access"
                `(?i)refresh`,      // Any variable with "refresh"
                `(?i)salt`,         // Any variable with "salt"
                `(?i)hash`,         // Any variable with "hash"
                `(?i)signature`,    // Any variable with "signature"
                `(?i)license`,      // Any variable with "license"
                `(?i)serial`,       // Any variable with "serial"
            },
        },
        
        HashSensitiveValues: true,
    }
}

// LoadEnvConfig loads configuration from a file, falling back to defaults
func LoadEnvConfig(configPath string) (*EnvConfig, error) {
    // Try to load from config file
    if configPath != "" {
        data, err := ioutil.ReadFile(configPath)
        if err != nil {
            return nil, fmt.Errorf("failed to read config file %s: %v", configPath, err)
        }
        
        var config EnvConfig
        if err := json.Unmarshal(data, &config); err != nil {
            return nil, fmt.Errorf("failed to parse config file %s: %v", configPath, err)
        }
        
        return &config, nil
    }
    
    // Try to load from default locations
    defaultPaths := []string{
        filepath.Join(os.Getenv("HOME"), ".totalrecall", "env-config.json"),
        filepath.Join(os.Getenv("HOME"), ".config", "totalrecall", "env-config.json"),
        "env-config.json",
    }
    
    for _, path := range defaultPaths {
        if data, err := ioutil.ReadFile(path); err == nil {
            var config EnvConfig
            if err := json.Unmarshal(data, &config); err == nil {
                return &config, nil
            }
        }
    }
    
    // Fall back to default configuration
    return DefaultEnvConfig(), nil
}

// SaveDefaultConfig saves the default configuration to a file
func SaveDefaultConfig(path string) error {
    config := DefaultEnvConfig()
    data, err := json.MarshalIndent(config, "", "  ")
    if err != nil {
        return fmt.Errorf("failed to marshal config: %v", err)
    }
    
    // Ensure directory exists
    dir := filepath.Dir(path)
    if err := os.MkdirAll(dir, 0755); err != nil {
        return fmt.Errorf("failed to create directory %s: %v", dir, err)
    }
    
    return ioutil.WriteFile(path, data, 0644)
}

// getMaskedEnvVar masks sensitive environment variable values efficiently
func getMaskedEnvVar(key string, value string) string {
    patterns := []string{"secret", "password", "key"}
    for _, pattern := range patterns {
        matched, err := regexp.MatchString("(?i)" + pattern, key)
        if err != nil {
            // If regex fails, be safe and hash anyway
            hash := sha256.Sum256([]byte(value))
            return fmt.Sprintf("h8_%x", hash[:4]) // 8 char hash
        }
        if matched {
            hash := sha256.Sum256([]byte(value))
            // Use shorter hash for better ES performance
            return fmt.Sprintf("h8_%x", hash[:4]) // 8 char hash instead of 64
        }
    }
    return value
}

// ShouldIncludeEnvVar determines if an environment variable should be included
func (config *EnvConfig) ShouldIncludeEnvVar(key, value string) (include bool, shouldHash bool) {
    // First check absolute denylist (things that should NEVER be included, even hashed)
    absoluteDenyPatterns := []string{
        `^___PREEXEC_`,     // Our temporary variables
        `^__`,              // Double underscore variables (usually internal)
        `^BASH_FUNC_`,      // Bash function exports
        `^_$`,              // Last argument of previous command
        `^PS[1-4]$`,        // Prompt strings
        `^TERM$`,           // Terminal type
        `^LINES$`, `^COLUMNS$`, // Terminal dimensions
        `^HIST`,            // History settings
        `^IFS$`,            // Internal field separator
        `^OPT`,             // getopt variables
        `^RANDOM$`,         // Random number
        `^SECONDS$`,        // Seconds since shell start
        `^BASH_`,           // Most bash internals
        `^FUNCNAME$`, `^PIPESTATUS$`, `^REPLY$`, // Bash internals
        `^SHELLOPTS$`, `^BASHOPTS$`, // Shell options
        `TOTALRECALLROOT`,  // Our own variable
    }
    
    for _, pattern := range absoluteDenyPatterns {
        if matched, _ := regexp.MatchString(pattern, key); matched {
            return false, false
        }
    }
    
    // Check user-defined absolute denylists
    for _, denied := range config.Denylist.Exact {
        if key == denied {
            return false, false
        }
    }
    
    // Check allowlist to see if this variable has value
    allowed := false
    
    // Check exact matches
    for _, allowed_var := range config.Allowlist.Exact {
        if key == allowed_var {
            allowed = true
            break
        }
    }
    
    // Check patterns if not already allowed
    if !allowed {
        for _, pattern := range config.Allowlist.Patterns {
            if matched, _ := regexp.MatchString(pattern, key); matched {
                allowed = true
                break
            }
        }
    }
    
    if !allowed {
        return false, false
    }
    
    // Now the variable is allowed, but should we hash it?
    
    // Check user-defined sensitive patterns (these get hashed but included)
    for _, pattern := range config.Denylist.Patterns {
        if matched, _ := regexp.MatchString(pattern, key); matched {
            return true, true  // Include but hash
        }
    }
    
    // Check built-in patterns that suggest the value might be sensitive
    if config.HashSensitiveValues {
        sensitivePatterns := []string{
            `(?i)password`,     // Any variable with "password"
            `(?i)secret`,       // Any variable with "secret"
            `(?i)key`,          // Any variable with "key" (API keys, etc.)
            `(?i)token`,        // Any variable with "token"
            `(?i)auth`,         // Any variable with "auth"
            `(?i)credential`,   // Any variable with "credential"
            `(?i)private`,      // Any variable with "private"
            `(?i)session`,      // Any variable with "session"
            `(?i)cookie`,       // Any variable with "cookie"
            `(?i)cert`,         // Any variable with "cert"
            `(?i)ssl`,          // Any variable with "ssl"
            `(?i)tls`,          // Any variable with "tls"
            `(?i)oauth`,        // Any variable with "oauth"
            `(?i)jwt`,          // Any variable with "jwt"
            `(?i)bearer`,       // Any variable with "bearer"
            `(?i)access`,       // Any variable with "access"
            `(?i)refresh`,      // Any variable with "refresh"
            `(?i)salt`,         // Any variable with "salt"
            `(?i)hash`,         // Any variable with "hash"
            `(?i)signature`,    // Any variable with "signature"
            `(?i)license`,      // Any variable with "license"
            `(?i)serial`,       // Any variable with "serial"
            `(?i)url`,          // URLs might contain credentials
            `(?i)dsn`,          // Database connection strings
            `(?i)connection`,   // Connection strings
            `(?i)endpoint`,     // API endpoints might be sensitive
        }
        
        for _, pattern := range sensitivePatterns {
            if matched, _ := regexp.MatchString(pattern, key); matched {
                return true, true  // Include but hash
            }
        }
    }
    
    return true, false
}

// FilterEnvironment filters environment variables according to the configuration
func (config *EnvConfig) FilterEnvironment(env map[string]string) map[string]string {
    filtered := make(map[string]string)
    
    for key, value := range env {
        if include, shouldHash := config.ShouldIncludeEnvVar(key, value); include {
            if shouldHash {
                hash := sha256.Sum256([]byte(value))
                filtered[key] = fmt.Sprintf("h8_%x", hash[:4]) // Shorter hash for ES efficiency
            } else {
                filtered[key] = value
            }
        }
    }
    
    return filtered
}
