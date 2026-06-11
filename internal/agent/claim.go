package agent

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
)

// DefaultClaimPort is the VSOCK port for vm.claim payloads.
const DefaultClaimPort uint32 = 2222

// ClaimServerConfig holds configuration for the claim listener.
type ClaimServerConfig struct {
	Port   uint32
	Logger *log.Logger
}

// ClaimPayload is the JSON message sent by the orchestrator.
type ClaimPayload struct {
	Action        string        `json:"action"`
	Hostname      string        `json:"hostname"`
	GuestUsername string        `json:"guest_username"`
	SSHKeys       []ClaimSSHKey `json:"ssh_keys"`
	StartupScript string        `json:"startup_script,omitempty"`
	Engine        string        `json:"engine,omitempty"`
	DatabaseName  string        `json:"database_name,omitempty"`
	Username      string        `json:"username,omitempty"`
	Password      string        `json:"password,omitempty"`
}

// ClaimSSHKey is an SSH public key.
type ClaimSSHKey struct {
	PublicKey string `json:"public_key"`
}

// HandleClaimPayload applies a ClaimPayload (SSH keys, hostname, startup script) to the VM.
func HandleClaimPayload(cfg ClaimServerConfig, p ClaimPayload) error {
	logger := cfg.Logger
	if logger == nil {
		logger = log.New(os.Stderr, "rumpty-agent claim: ", log.LstdFlags)
	}

	switch strings.TrimSpace(p.Action) {
	case "claim":
		return handleVMClaimPayload(p, logger)
	case "database.claim":
		return handleDatabaseClaimPayload(p, logger)
	default:
		return fmt.Errorf("unknown action %q", p.Action)
	}
}

func handleVMClaimPayload(p ClaimPayload, logger *log.Logger) error {
	if err := applySSHKeys(p.GuestUsername, p.SSHKeys, logger); err != nil {
		return fmt.Errorf("apply ssh keys: %w", err)
	}

	if hostname := strings.TrimSpace(p.Hostname); hostname != "" {
		if err := applyHostname(hostname, logger); err != nil {
			// Non-fatal — VM is still usable with the old hostname.
			logger.Printf("warn: set hostname %q: %v", hostname, err)
		}
	}

	if script := strings.TrimSpace(p.StartupScript); script != "" {
		if err := applyStartupScript(script, logger); err != nil {
			logger.Printf("warn: apply startup script: %v", err)
		}
	}

	return nil
}

func handleDatabaseClaimPayload(p ClaimPayload, logger *log.Logger) error {
	switch strings.ToLower(strings.TrimSpace(p.Engine)) {
	case "postgres", "postgresql":
		return applyPostgresClaim(p.DatabaseName, p.Username, p.Password, logger)
	case "mysql":
		return applyMySQLClaim(p.DatabaseName, p.Username, p.Password, logger)
	case "redis":
		return applyRedisClaim(p.Password, logger)
	default:
		return fmt.Errorf("unsupported database claim engine %q", p.Engine)
	}
}

func applyPostgresClaim(databaseName string, username string, password string, logger *log.Logger) error {
	databaseName = strings.TrimSpace(databaseName)
	username = strings.TrimSpace(username)
	if databaseName == "" || username == "" || password == "" {
		return fmt.Errorf("database_name, username, and password are required")
	}

	if out, err := exec.Command("systemctl", "is-active", "--quiet", "postgresql").CombinedOutput(); err != nil {
		logger.Printf("postgresql is not active, attempting start: %v — %s", err, out)
		if startOut, startErr := exec.Command("systemctl", "start", "postgresql").CombinedOutput(); startErr != nil {
			return fmt.Errorf("start postgresql: %w — %s", startErr, startOut)
		}
	}

	sql := fmt.Sprintf(`
ALTER USER %s WITH PASSWORD %s;
SELECT 'CREATE DATABASE %s OWNER %s' WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = %s)\gexec
GRANT ALL PRIVILEGES ON DATABASE %s TO %s;
`, sqlIdentifier(username), sqlLiteral(password), sqlIdentifier(databaseName), sqlIdentifier(username), sqlLiteral(databaseName), sqlIdentifier(databaseName), sqlIdentifier(username))

	cmd := exec.Command("runuser", "-u", "postgres", "--", "psql", "-v", "ON_ERROR_STOP=1")
	cmd.Stdin = strings.NewReader(sql)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("apply postgres claim: %w — %s", err, out)
	}

	logger.Printf("postgres database claim applied database=%q username=%q", databaseName, username)
	return nil
}

func applyMySQLClaim(databaseName string, username string, password string, logger *log.Logger) error {
	databaseName = strings.TrimSpace(databaseName)
	username = strings.TrimSpace(username)
	if databaseName == "" || username == "" || password == "" {
		return fmt.Errorf("database_name, username, and password are required")
	}

	service := "mysql"
	if out, err := exec.Command("systemctl", "is-active", "--quiet", service).CombinedOutput(); err != nil {
		logger.Printf("mysql is not active, trying mariadb/start: %v — %s", err, out)
		if startOut, startErr := exec.Command("systemctl", "start", service).CombinedOutput(); startErr != nil {
			service = "mariadb"
			if mariaOut, mariaErr := exec.Command("systemctl", "start", service).CombinedOutput(); mariaErr != nil {
				return fmt.Errorf("start mysql/mariadb: %w — %s; mysql output: %s", mariaErr, mariaOut, startOut)
			}
		}
	}

	configureMySQLNetwork(logger)
	_ = exec.Command("systemctl", "restart", service).Run()

	sql := fmt.Sprintf(`
CREATE DATABASE IF NOT EXISTS %s;
CREATE USER IF NOT EXISTS %s@'%%' IDENTIFIED BY %s;
ALTER USER %s@'%%' IDENTIFIED BY %s;
GRANT ALL PRIVILEGES ON %s.* TO %s@'%%';
FLUSH PRIVILEGES;
`, mysqlIdentifier(databaseName), mysqlLiteral(username), mysqlLiteral(password), mysqlLiteral(username), mysqlLiteral(password), mysqlIdentifier(databaseName), mysqlLiteral(username))

	cmd := exec.Command("mysql", "-uroot")
	cmd.Stdin = strings.NewReader(sql)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("apply mysql claim: %w — %s", err, out)
	}

	logger.Printf("mysql database claim applied database=%q username=%q", databaseName, username)
	return nil
}

func configureMySQLNetwork(logger *log.Logger) {
	for _, path := range []string{"/etc/mysql/mysql.conf.d/mysqld.cnf", "/etc/mysql/mariadb.conf.d/50-server.cnf"} {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var out strings.Builder
		changed := false
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "bind-address") {
				out.WriteString("bind-address = 0.0.0.0\n")
				changed = true
				continue
			}
			out.WriteString(line)
			out.WriteByte('\n')
		}
		if changed {
			if err := os.WriteFile(path, []byte(out.String()), 0o644); err != nil {
				logger.Printf("warn: update mysql bind-address %s: %v", path, err)
			}
		}
	}
}

func applyRedisClaim(password string, logger *log.Logger) error {
	if strings.TrimSpace(password) == "" {
		return fmt.Errorf("password is required")
	}

	service := "redis-server"
	if out, err := exec.Command("systemctl", "is-active", "--quiet", service).CombinedOutput(); err != nil {
		logger.Printf("redis-server is not active, trying redis/start: %v — %s", err, out)
		if startOut, startErr := exec.Command("systemctl", "start", service).CombinedOutput(); startErr != nil {
			service = "redis"
			if redisOut, redisErr := exec.Command("systemctl", "start", service).CombinedOutput(); redisErr != nil {
				return fmt.Errorf("start redis: %w — %s; redis-server output: %s", redisErr, redisOut, startOut)
			}
		}
	}

	conf, err := findRedisConfig()
	if err != nil {
		return err
	}
	if err := rewriteRedisConfig(conf, password); err != nil {
		return err
	}
	if out, err := exec.Command("systemctl", "restart", service).CombinedOutput(); err != nil {
		return fmt.Errorf("restart redis: %w — %s", err, out)
	}

	logger.Printf("redis claim applied")
	return nil
}

func findRedisConfig() (string, error) {
	for _, path := range []string{"/etc/redis/redis.conf", "/etc/redis.conf"} {
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("redis config not found")
}

func rewriteRedisConfig(path string, password string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read redis config: %w", err)
	}
	var out strings.Builder
	seenRequirePass := false
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "bind "):
			out.WriteString("bind 0.0.0.0 ::\n")
		case strings.HasPrefix(trimmed, "protected-mode "):
			out.WriteString("protected-mode no\n")
		case strings.HasPrefix(trimmed, "requirepass "):
			out.WriteString("requirepass " + password + "\n")
			seenRequirePass = true
		default:
			out.WriteString(line)
			out.WriteByte('\n')
		}
	}
	if !seenRequirePass {
		out.WriteString("\nrequirepass " + password + "\n")
	}
	if err := os.WriteFile(path, []byte(out.String()), 0o644); err != nil {
		return fmt.Errorf("write redis config: %w", err)
	}
	return nil
}

func sqlIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func sqlLiteral(value string) string {
	return `'` + strings.ReplaceAll(value, `'`, `''`) + `'`
}

func mysqlIdentifier(value string) string {
	return "`" + strings.ReplaceAll(value, "`", "``") + "`"
}

func mysqlLiteral(value string) string {
	return `'` + strings.ReplaceAll(value, `'`, `''`) + `'`
}

// applySSHKeys configures the authorized_keys file for the guest user.
func applySSHKeys(username string, keys []ClaimSSHKey, logger *log.Logger) error {
	if strings.TrimSpace(username) == "" {
		username = "rumpty"
	}

	homeDir, err := userHomeDir(username)
	if err != nil {
		return fmt.Errorf("resolve home dir for %q: %w", username, err)
	}

	if _, statErr := os.Stat(homeDir); os.IsNotExist(statErr) {
		logger.Printf("home dir %q missing — creating it for user %q", homeDir, username)
		if out, mkErr := exec.Command("mkhomedir_helper", username).CombinedOutput(); mkErr != nil {
			logger.Printf("warn: mkhomedir_helper %q: %v — %s, falling back to mkdir", username, mkErr, out)
			if mkErr2 := os.MkdirAll(homeDir, 0o750); mkErr2 != nil {
				return fmt.Errorf("create home dir %s: %w", homeDir, mkErr2)
			}
			if out, chErr := exec.Command("chown", username+":"+username, homeDir).CombinedOutput(); chErr != nil {
				logger.Printf("warn: chown home dir %s: %v — %s", homeDir, chErr, out)
			}
		}
	}

	sshDir := homeDir + "/.ssh"
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		return fmt.Errorf("mkdir %s: %w", sshDir, err)
	}

	// Build the authorized_keys content.
	var b strings.Builder
	for _, k := range keys {
		pk := strings.TrimSpace(k.PublicKey)
		if pk == "" {
			continue
		}
		b.WriteString(pk)
		b.WriteByte('\n')
	}

	authKeysPath := sshDir + "/authorized_keys"
	if err := os.WriteFile(authKeysPath, []byte(b.String()), 0o600); err != nil {
		return fmt.Errorf("write authorized_keys: %w", err)
	}

	// Fix ownership.
	if out, err := exec.Command("chown", "-R", username+":"+username, sshDir).CombinedOutput(); err != nil {
		logger.Printf("warn: chown %s: %v — %s", sshDir, err, out)
	}

	logger.Printf("authorized_keys updated for user %q (%d keys)", username, len(keys))
	return nil
}

func applyHostname(hostname string, logger *log.Logger) error {
	if err := os.WriteFile("/etc/hostname", []byte(hostname+"\n"), 0o644); err != nil {
		logger.Printf("warn: write /etc/hostname: %v", err)
	}
	if out, err := exec.Command("hostnamectl", "set-hostname", hostname).CombinedOutput(); err != nil {
		logger.Printf("warn: hostnamectl set-hostname %q: %v — %s", hostname, err, out)
		_ = os.WriteFile("/proc/sys/kernel/hostname", []byte(hostname), 0o644)
	}

	updateEtcHosts(hostname, logger)

	logger.Printf("hostname set to %q", hostname)
	return nil
}

// updateEtcHosts updates the hostname mapping in /etc/hosts.
func updateEtcHosts(hostname string, logger *log.Logger) {
	data, err := os.ReadFile("/etc/hosts")
	if err != nil {
		logger.Printf("warn: read /etc/hosts: %v", err)
		return
	}

	var out strings.Builder
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "127.0.1.1") {
			out.WriteString("127.0.1.1\t" + hostname + "\n")
			continue
		}
		out.WriteString(line + "\n")
	}

	if err := os.WriteFile("/etc/hosts", []byte(out.String()), 0o644); err != nil {
		logger.Printf("warn: write /etc/hosts: %v", err)
	}
}

// applyStartupScript writes and executes the startup script in the background.
func applyStartupScript(script string, logger *log.Logger) error {
	const scriptPath = "/var/lib/rumpty/startup.sh"
	if err := os.MkdirAll("/var/lib/rumpty", 0o755); err != nil {
		return fmt.Errorf("mkdir /var/lib/rumpty: %w", err)
	}
	if err := os.WriteFile(scriptPath, []byte(script), 0o700); err != nil {
		return fmt.Errorf("write startup script: %w", err)
	}

	go func() {
		cmd := exec.Command("bash", scriptPath)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			logger.Printf("startup script exited with error: %v", err)
		} else {
			logger.Printf("startup script completed successfully")
		}
	}()

	logger.Printf("startup script written to %s and launched", scriptPath)
	return nil
}

// userHomeDir resolves the home directory from /etc/passwd.
func userHomeDir(username string) (string, error) {
	data, err := os.ReadFile("/etc/passwd")
	if err != nil {
		return "", err
	}

	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Split(line, ":")
		if len(fields) >= 6 && fields[0] == username {
			homeDir := fields[5]
			if homeDir != "" {
				return homeDir, nil
			}
		}
	}

	return "/home/" + username, nil
}

// HandleClaimConn processes a single inbound claim connection.
func HandleClaimConn(cfg ClaimServerConfig, conn interface {
	Read([]byte) (int, error)
	Write([]byte) (int, error)
	Close() error
}) {
	logger := cfg.Logger
	if logger == nil {
		logger = log.New(os.Stderr, "rumpty-agent claim: ", log.LstdFlags)
	}

	defer conn.Close()

	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 64*1024), 64*1024)

	if !scanner.Scan() {
		err := scanner.Err()
		if err != nil {
			logger.Printf("read claim payload: %v", err)
			_, _ = conn.Write([]byte("err: read error\n"))
		}
		return
	}

	line := scanner.Bytes()
	var payload ClaimPayload
	if err := json.Unmarshal(line, &payload); err != nil {
		logger.Printf("decode claim payload: %v", err)
		_, _ = conn.Write([]byte("err: invalid json\n"))
		return
	}

	if err := HandleClaimPayload(cfg, payload); err != nil {
		logger.Printf("handle claim payload: %v", err)
		msg := fmt.Sprintf("err: %s\n", err.Error())
		_, _ = conn.Write([]byte(msg))
		return
	}

	_, _ = conn.Write([]byte("ok\n"))
	logger.Printf("claim handled successfully (action=%q hostname=%q user=%q keys=%d)",
		payload.Action, payload.Hostname, payload.GuestUsername, len(payload.SSHKeys))
}
