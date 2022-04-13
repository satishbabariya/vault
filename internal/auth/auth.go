package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"ssh-vault/internal/config"
	"ssh-vault/internal/proto"
	"ssh-vault/internal/store"
	"time"

	"github.com/golang-jwt/jwt"
	"github.com/google/go-github/v43/github"
)

type AuthServiceServer struct {
	proto.UnimplementedAuthServiceServer
	config *config.Config
	store  *store.Store
}

func NewAuthServiceServer(config *config.Config, store *store.Store) *AuthServiceServer {
	return &AuthServiceServer{
		config: config,
		store:  store,
	}
}

func (s *AuthServiceServer) GetConfig(context.Context, *proto.Empty) (*proto.AuthConfigResponse, error) {
	fmt.Println(s.config)
	return &proto.AuthConfigResponse{
		GithubHost:     s.config.GitHubHost,
		GithubClientId: s.config.GithubClientID,
	}, nil
}

func (s *AuthServiceServer) Authenticate(ctx context.Context, in *proto.AuthenticateRequest) (*proto.AuthenticateResponse, error) {
	url := "https://api.github.com/user"
	method := "GET"

	client := &http.Client{}
	req, err := http.NewRequest(method, url, nil)

	if err != nil {
		return nil, err
	}

	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}

	var user github.User
	err = json.Unmarshal(body, &user)
	if err != nil {
		return nil, err
	}

	t := jwt.New(jwt.GetSigningMethod("HS256"))
	t.Claims = jwt.MapClaims{
		"iss": user.GetLogin(),
		"exp": time.Now().Add(time.Hour * 24).Unix(),
	}

	token, err := t.SignedString(
		[]byte(s.config.VaultSecret),
	)
	if err != nil {
		return nil, err
	}

	return &proto.AuthenticateResponse{
		Token: token,
	}, nil
}
