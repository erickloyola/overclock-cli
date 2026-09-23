package memory

import (
	"strings"
	"testing"
)

func TestExtractPublicAPIGo(t *testing.T) {
	goCode := `package service

import (
	"context"
	"fmt"
)

type User struct {
	ID    int64  ` + "`json:\"id\"`" + `
	Name  string ` + "`json:\"name\"`" + `
	Email string ` + "`json:\"email\"`" + `
}

type UserService interface {
	GetUser(ctx context.Context, id int64) (*User, error)
	CreateUser(ctx context.Context, u *User) error
}

func (s *userService) GetUser(ctx context.Context, id int64) (*User, error) {
	// 50 lines of complex database queries and logs...
	fmt.Println("loading user")
	return nil, nil
}

func (s *userService) internalHelper() {
	// private helper
}

func NewService() UserService {
	return &userService{}
}
`
	// Make code > 45 lines to trigger extraction
	for i := 0; i < 30; i++ {
		goCode += "// extra lines to simulate real file size\n"
	}

	api := ExtractPublicAPI("service.go", goCode)

	if !strings.Contains(api, "type User struct") {
		t.Errorf("Esperava struct User no PublicAPI")
	}
	if !strings.Contains(api, "type UserService interface") {
		t.Errorf("Esperava interface UserService no PublicAPI")
	}
	if !strings.Contains(api, "func NewService()") {
		t.Errorf("Esperava func NewService no PublicAPI")
	}
	if strings.Contains(api, "internalHelper") {
		t.Errorf("Não esperava método privado internalHelper no PublicAPI")
	}
	if strings.Contains(api, "loading user") {
		t.Errorf("Não esperava corpo da função no PublicAPI")
	}
}

func TestExtractPublicAPITs(t *testing.T) {
	tsCode := `import { BaseEntity } from './base';

export interface UserProfile {
	id: string;
	username: string;
	roles: string[];
}

export type AuthToken = string;

export function validateToken(token: AuthToken): boolean {
	const parts = token.split('.');
	return parts.length === 3;
}

function privateHelper() {
	console.log("secret");
}
`
	for i := 0; i < 35; i++ {
		tsCode += "// padding line\n"
	}

	api := ExtractPublicAPI("src/auth.ts", tsCode)
	if !strings.Contains(api, "export interface UserProfile") {
		t.Errorf("Esperava export interface UserProfile")
	}
	if !strings.Contains(api, "export type AuthToken") {
		t.Errorf("Esperava export type AuthToken")
	}
	if !strings.Contains(api, "export function validateToken(token: AuthToken): boolean;") {
		t.Errorf("Esperava assinatura de validateToken, obteve:\n%s", api)
	}
	if strings.Contains(api, "privateHelper") {
		t.Errorf("Não esperava função privada no PublicAPI")
	}
}
