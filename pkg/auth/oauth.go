package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	GoogleAuthURL   = "https://accounts.google.com/o/oauth2/v2/auth"
	GoogleTokenURL  = "https://oauth2.googleapis.com/token"
	GoogleUserInfo  = "https://www.googleapis.com/oauth2/v2/userinfo"
	DefaultClientID = "936475272427-7nmgst74ha7v2m9905206q9elopvsn7p.apps.googleusercontent.com" // Standard Google Public CLI Client ID
	DefaultScope    = "https://www.googleapis.com/auth/generative-language https://www.googleapis.com/auth/cloud-platform https://www.googleapis.com/auth/userinfo.email"
)

// OAuthAccount represents a persistent Google authenticated account profile.
type OAuthAccount struct {
	Name         string    `json:"name"`
	Email        string    `json:"email"`
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	Expiry       time.Time `json:"expiry"`
	TokenType    string    `json:"token_type"`
	ClientID     string    `json:"client_id,omitempty"`
	ClientSecret string    `json:"client_secret,omitempty"`
	mu           sync.Mutex `json:"-"`
}

// GetStorageDir returns ~/.config/overclock/accounts
func GetStorageDir() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		configDir = filepath.Join(home, ".config")
	}
	dir := filepath.Join(configDir, "overclock", "accounts")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	return dir, nil
}

// Save persists the account to disk with secure 0600 permissions.
func (a *OAuthAccount) Save() error {
	dir, err := GetStorageDir()
	if err != nil {
		return err
	}
	filePath := filepath.Join(dir, a.Name+".json")
	data, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filePath, data, 0600)
}

// LoadAccount loads a specific OAuth profile by name.
func LoadAccount(name string) (*OAuthAccount, error) {
	dir, err := GetStorageDir()
	if err != nil {
		return nil, err
	}
	filePath := filepath.Join(dir, name+".json")
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}
	var acc OAuthAccount
	if err := json.Unmarshal(data, &acc); err != nil {
		return nil, err
	}
	return &acc, nil
}

// ListAccounts loads all authenticated OAuth accounts from disk.
func ListAccounts() ([]*OAuthAccount, error) {
	dir, err := GetStorageDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var accounts []*OAuthAccount
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
			name := strings.TrimSuffix(entry.Name(), ".json")
			acc, err := LoadAccount(name)
			if err == nil && acc.RefreshToken != "" {
				accounts = append(accounts, acc)
			}
		}
	}
	return accounts, nil
}

// RemoveAccount deletes an account credential file.
func RemoveAccount(name string) error {
	dir, err := GetStorageDir()
	if err != nil {
		return err
	}
	filePath := filepath.Join(dir, name+".json")
	return os.Remove(filePath)
}

// GetValidToken returns a fresh, valid Access Token, automatically refreshing if expired.
func (a *OAuthAccount) GetValidToken() (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	// If token has at least 2 minutes remaining, reuse it
	if time.Now().Before(a.Expiry.Add(-2 * time.Minute)) && a.AccessToken != "" {
		return a.AccessToken, nil
	}

	// Token expired or close to expiry: refresh it
	if a.RefreshToken == "" {
		return "", fmt.Errorf("conta '%s' não possui refresh_token. Execute: overclock login --name %s", a.Name, a.Name)
	}

	clientID := a.ClientID
	if clientID == "" {
		clientID = os.Getenv("GOOGLE_CLIENT_ID")
		if clientID == "" {
			clientID = DefaultClientID
		}
	}
	clientSecret := a.ClientSecret
	if clientSecret == "" {
		clientSecret = os.Getenv("GOOGLE_CLIENT_SECRET")
	}

	data := url.Values{}
	data.Set("client_id", clientID)
	if clientSecret != "" {
		data.Set("client_secret", clientSecret)
	}
	data.Set("refresh_token", a.RefreshToken)
	data.Set("grant_type", "refresh_token")

	resp, err := http.PostForm(GoogleTokenURL, data)
	if err != nil {
		return "", fmt.Errorf("falha ao renovar token OAuth: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("erro ao renovar token Google (HTTP %d): %s", resp.StatusCode, string(body))
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		TokenType   string `json:"token_type"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return "", fmt.Errorf("falha ao decodificar token renovado: %w", err)
	}

	a.AccessToken = tokenResp.AccessToken
	a.TokenType = tokenResp.TokenType
	a.Expiry = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)

	_ = a.Save() // Persist updated token to disk

	return a.AccessToken, nil
}

// StartLoginFlow initiates the interactive browser OAuth 2.0 flow.
func StartLoginFlow(accountName, clientID, clientSecret, scope string) (*OAuthAccount, error) {
	if accountName == "" {
		accountName = "default"
	}
	if clientID == "" {
		clientID = os.Getenv("GOOGLE_CLIENT_ID")
		if clientID == "" {
			clientID = DefaultClientID
		}
	}
	if clientSecret == "" {
		clientSecret = os.Getenv("GOOGLE_CLIENT_SECRET")
	}
	if scope == "" {
		scope = os.Getenv("GOOGLE_OAUTH_SCOPE")
		if scope == "" {
			scope = DefaultScope
		}
	}

	// Find an available local port
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("não foi possível abrir porta local: %w", err)
	}
	defer listener.Close()

	port := listener.Addr().(*net.TCPAddr).Port
	redirectURI := fmt.Sprintf("http://127.0.0.1:%d/oauth/callback", port)

	authURLParams := url.Values{}
	authURLParams.Set("client_id", clientID)
	authURLParams.Set("redirect_uri", redirectURI)
	authURLParams.Set("response_type", "code")
	authURLParams.Set("scope", scope)
	authURLParams.Set("access_type", "offline")
	authURLParams.Set("prompt", "consent") // Force refresh token return

	fullAuthURL := fmt.Sprintf("%s?%s", GoogleAuthURL, authURLParams.Encode())

	codeChan := make(chan string, 1)
	errChan := make(chan error, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/callback", func(w http.ResponseWriter, r *http.Request) {
		code := r.URL.Query().Get("code")
		if code == "" {
			errDesc := r.URL.Query().Get("error_description")
			if errDesc == "" {
				errDesc = r.URL.Query().Get("error")
			}
			http.Error(w, "Falha na autorização: "+errDesc, http.StatusBadRequest)
			errChan <- errors.New(errDesc)
			return
		}

		// Display a sleek, dark-themed confirmation page
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `
<!DOCTYPE html>
<html>
<head>
  <meta charset="utf-8">
  <title>Overclock CLI - Autenticado</title>
  <style>
    body { background: #0f172a; color: #f8fafc; font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; display: flex; align-items: center; justify-content: center; height: 100vh; margin: 0; }
    .card { background: #1e293b; border: 1px solid #334155; border-radius: 16px; padding: 40px; text-align: center; max-width: 480px; box-shadow: 0 25px 50px -12px rgba(0, 0, 0, 0.5); }
    h1 { color: #38bdf8; margin-top: 0; font-size: 24px; display: flex; align-items: center; justify-content: center; gap: 8px; }
    p { color: #94a3b8; font-size: 15px; line-height: 1.6; }
    .badge { display: inline-block; background: #065f46; color: #34d399; font-weight: 600; padding: 6px 14px; border-radius: 9999px; margin-bottom: 20px; font-size: 13px; }
  </style>
</head>
<body>
  <div class="card">
    <div class="badge">⚡ OVERCLOCK CLI</div>
    <h1>Autenticado com Sucesso!</h1>
    <p>Sua conta Google foi conectada ao <strong>Overclock CLI</strong> com cotas OAuth ativas.<br>Você já pode fechar esta aba e retornar ao seu terminal.</p>
  </div>
</body>
</html>`)

		codeChan <- code
	})

	server := &http.Server{Handler: mux}
	go func() {
		_ = server.Serve(listener)
	}()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	}()

	fmt.Println("\n🔗 Abrindo navegador para autorização Google...")
	fmt.Printf("Se o navegador não abrir automaticamente, copie e cole este link:\n\n%s\n\n", fullAuthURL)

	// Attempt to open default browser
	_ = exec.Command("xdg-open", fullAuthURL).Start()

	// Wait for authorization code or timeout
	select {
	case <-time.After(3 * time.Minute):
		return nil, errors.New("tempo limite esgotado aguardando autorização no navegador (3 minutos)")
	case err := <-errChan:
		return nil, fmt.Errorf("autorização cancelada ou com erro: %w", err)
	case code := <-codeChan:
		// Exchange code for tokens
		tokenData := url.Values{}
		tokenData.Set("code", code)
		tokenData.Set("client_id", clientID)
		if clientSecret != "" {
			tokenData.Set("client_secret", clientSecret)
		}
		tokenData.Set("redirect_uri", redirectURI)
		tokenData.Set("grant_type", "authorization_code")

		resp, err := http.PostForm(GoogleTokenURL, tokenData)
		if err != nil {
			return nil, fmt.Errorf("falha ao trocar código de autorização por token: %w", err)
		}
		defer resp.Body.Close()

		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("erro do Google ao gerar tokens (HTTP %d): %s", resp.StatusCode, string(body))
		}

		var tr struct {
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
			ExpiresIn    int    `json:"expires_in"`
			TokenType    string `json:"token_type"`
		}
		if err := json.Unmarshal(body, &tr); err != nil {
			return nil, fmt.Errorf("resposta de token inválida: %w", err)
		}

		// Fetch user email
		email := fetchUserEmail(tr.AccessToken)

		account := &OAuthAccount{
			Name:         accountName,
			Email:        email,
			AccessToken:  tr.AccessToken,
			RefreshToken: tr.RefreshToken,
			Expiry:       time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second),
			TokenType:    tr.TokenType,
			ClientID:     clientID,
			ClientSecret: clientSecret,
		}

		if err := account.Save(); err != nil {
			return nil, fmt.Errorf("falha ao salvar credenciais da conta: %w", err)
		}

		return account, nil
	}
}

func fetchUserEmail(accessToken string) string {
	req, err := http.NewRequest(http.MethodGet, GoogleUserInfo, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return ""
	}

	var ui struct {
		Email string `json:"email"`
	}
	body, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(body, &ui)
	return ui.Email
}
