// package main

// import (
// 	"context"
// 	"flag"
// 	"fmt"
// 	"log"
// 	"os"
// 	"os/signal"
// 	"syscall"
// 	"time"

// 	"golang.org/x/crypto/ssh"
// 	"golang.org/x/term"
// )

// var (
// 	user     = flag.String("l", "", "login_name")
// 	password = flag.String("pass", "", "password")
// 	port     = flag.Int("p", 22, "port")
// )

// func main() {
// 	flag.Parse()
// 	if flag.NArg() == 0 {
// 		flag.Usage()
// 		os.Exit(2)
// 	}

// 	sig := make(chan os.Signal, 1)
// 	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
// 	ctx, cancel := context.WithCancel(context.Background())

// 	go func() {
// 		if err := run(ctx); err != nil {
// 			log.Print(err)
// 		}
// 		cancel()
// 	}()

// 	select {
// 	case <-sig:
// 		cancel()
// 	case <-ctx.Done():
// 	}
// }

// func run(ctx context.Context) error {
// 	fmt.Println("Connecting...", *user, *password, *port)
// 	config := &ssh.ClientConfig{
// 		User: *user,
// 		Auth: []ssh.AuthMethod{
// 			ssh.Password(*password),
// 		},
// 		Timeout:         5 * time.Second,
// 		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
// 	}

// 	hostport := fmt.Sprintf("%s:%d", flag.Arg(0), *port)
// 	conn, err := ssh.Dial("tcp", hostport, config)
// 	if err != nil {
// 		return fmt.Errorf("cannot connect %v: %v", hostport, err)
// 	}
// 	defer conn.Close()

// 	session, err := conn.NewSession()
// 	if err != nil {
// 		return fmt.Errorf("cannot open new session: %v", err)
// 	}
// 	defer session.Close()

// 	go func() {
// 		<-ctx.Done()
// 		conn.Close()
// 	}()

// 	fd := int(os.Stdin.Fd())
// 	state, err := term.MakeRaw(fd)
// 	if err != nil {
// 		return fmt.Errorf("terminal make raw: %s", err)
// 	}
// 	defer term.Restore(fd, state)

// 	w, h, err := term.GetSize(fd)
// 	if err != nil {
// 		return fmt.Errorf("terminal get size: %s", err)
// 	}

// 	modes := ssh.TerminalModes{
// 		ssh.ECHO:          1,
// 		ssh.TTY_OP_ISPEED: 14400,
// 		ssh.TTY_OP_OSPEED: 14400,
// 	}

// 	term := os.Getenv("TERM")
// 	if term == "" {
// 		term = "xterm-256color"
// 	}
// 	if err := session.RequestPty(term, h, w, modes); err != nil {
// 		return fmt.Errorf("session xterm: %s", err)
// 	}

// 	session.Stdout = os.Stdout
// 	session.Stderr = os.Stderr
// 	session.Stdin = os.Stdin

// 	if err := session.Shell(); err != nil {
// 		return fmt.Errorf("session shell: %s", err)
// 	}

// 	if err := session.Wait(); err != nil {
// 		if e, ok := err.(*ssh.ExitError); ok {
// 			switch e.ExitStatus() {
// 			case 130:
// 				return nil
// 			}
// 		}
// 		return fmt.Errorf("ssh: %s", err)
// 	}
// 	return nil
// }

package main

import (
	"context"
	"fmt"
	"ssh-vault/internal/proto"
	"time"

	"github.com/cli/oauth"
	"github.com/joho/godotenv"
	"github.com/sirupsen/logrus"
	"github.com/zalando/go-keyring"
	"google.golang.org/grpc"
	"gopkg.in/square/go-jose.v2/jwt"
)

func init() {
	godotenv.Load()
}

type VaultClient struct {
	conn   *grpc.ClientConn
	client proto.AuthServiceClient
}

func main() {

	conn, err := grpc.Dial(
		"localhost:1203",
		grpc.WithInsecure(),
		grpc.WithBlock(),
		// grpc.WithUnaryInterceptor(interceptor.UnaryClientInterceptor),
		// grpc.WithStreamInterceptor(interceptor.StreamClientInterceptor),
	)
	if err != nil {
		logrus.Fatalf("failed to dial: %v", err)
	}

	client := proto.NewAuthServiceClient(conn)

	vault := &VaultClient{
		conn:   conn,
		client: client,
	}

	token, err := keyring.Get("vault", "token")
	if err != nil {
		t, err := vault.Login()
		if err != nil {
			logrus.Fatalf("failed to login: %v", err)
		}
		token = *t
	}

	t, err := jwt.ParseSigned(token)
	if err != nil {
		logrus.Fatalf("failed to parse token: %v", err)
	}

	var claims jwt.Claims

	err = t.UnsafeClaimsWithoutVerification(&claims)
	if err != nil {
		logrus.Fatalf("failed to parse token: %v", err)
	}

	// check if token is expired
	if claims.Expiry.Time().Before(time.Now()) {
		t, err := vault.Login()
		if err != nil {
			logrus.Fatalf("failed to login: %v", err)
		}
		token = *t
	}

	fmt.Println(token)
}

func (v *VaultClient) Login() (*string, error) {
	t, err := v.Authenticate(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to store token: %v", err)
	}

	err = keyring.Set(
		"vault", "token", *t,
	)

	if err != nil {
		return nil, fmt.Errorf("failed to store token: %v", err)
	}

	return t, nil
}

func (v *VaultClient) Authenticate(ctx context.Context) (*string, error) {
	config, err := v.client.GetConfig(ctx, &proto.Empty{})
	if err != nil {
		return nil, err
	}

	if config.GithubClientId == "" {
		return nil, fmt.Errorf("github client id is empty")
	}

	flow := &oauth.Flow{
		Host:     oauth.GitHubHost(config.GithubHost),
		ClientID: config.GithubClientId,
		Scopes: []string{
			"user:email",
		},
	}

	accessToken, err := flow.DeviceFlow()
	if err != nil {
		return nil, err
	}

	resp, err := v.client.Authenticate(context.Background(), &proto.AuthenticateRequest{
		Token: accessToken.Token,
	})

	if err != nil {
		return nil, err
	}

	if resp == nil {
		return nil, fmt.Errorf("authenticate response is nil")
	}

	return &resp.Token, nil
}
