package auth

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// SwitchAccount loads the named account from ~/.config/overclock/accounts/<name>.json
// and persists its credentials to the system Secret Service (service: gemini, username: antigravity)
// so the local agy CLI uses it immediately.
func SwitchAccount(name string) (*OAuthAccount, error) {
	acc, err := LoadAccount(name)
	if err != nil {
		return nil, fmt.Errorf("conta '%s' não encontrada: %w", name, err)
	}

	payload := map[string]interface{}{
		"token": map[string]interface{}{
			"access_token":  acc.AccessToken,
			"refresh_token": acc.RefreshToken,
			"token_type":    "Bearer",
			"expiry":        acc.Expiry.Format(time.RFC3339Nano),
		},
		"auth_method": "consumer",
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	cmd := exec.Command("secret-tool", "store", "--label=Password for 'antigravity' on 'gemini'", "service", "gemini", "username", "antigravity")
	cmd.Stdin = strings.NewReader(string(data))
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("falha ao atualizar secret-tool: %w (saída: %s)", err, string(out))
	}

	return acc, nil
}

// GetCurrentAgyEmail queries secret-tool to discover which account is currently active.
func GetCurrentAgyEmail() (string, error) {
	cmd := exec.Command("secret-tool", "lookup", "service", "gemini", "username", "antigravity")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}

	var data struct {
		Token struct {
			AccessToken string `json:"access_token"`
		} `json:"token"`
	}

	if err := json.Unmarshal(out, &data); err != nil {
		return "", err
	}

	// Compare with saved accounts to find matching email
	accounts, _ := ListAccounts()
	for _, acc := range accounts {
		if acc.AccessToken == data.Token.AccessToken {
			return acc.Email, nil
		}
	}

	if len(accounts) > 0 {
		return accounts[0].Email, nil
	}

	return "Conta Google Ativa", nil
}
