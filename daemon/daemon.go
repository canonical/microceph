package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"

	"golang.org/x/sys/unix"

	"github.com/canonical/go-dqlite/app"

	"github.com/canonical/microceph/network"
)

type Daemon struct {
}

func newDaemon() *Daemon {
	return &Daemon{}
}

func main() {
	var cluster []string

	myIP, err := network.GetHostDefaultAddr()
	if err != nil {
		// FIXME
	}

	// Set up dqlite
	app, err := app.New("/tmp",
		app.WithAddress(net.JoinHostPort(myIP.String(), "54276")),
		app.WithCluster(cluster))
	if err != nil {
		// FIXME
		fmt.Printf("HELP! %s\n", err)
		return
	}
	db, err := app.Open(context.Background(), "microceph")
	if err != nil {
		// FIXME
	}
	_, err = db.Exec("CREATE TABLE IF NOT EXISTS table (key, varchar(255), value varchar(255), UNIQUE(key))")
	if err != nil {
		// FIXME
	}
	// Set up signal handlnig
	ch := make(chan os.Signal)
	signal.Notify(ch, unix.SIGPWR)
	signal.Notify(ch, unix.SIGINT)
	signal.Notify(ch, unix.SIGTERM)
	signal.Notify(ch, unix.SIGQUIT)
	signal.Ignore(unix.SIGHUP)

	select {
	case sig := <-ch:
		fmt.Printf("Received '%s signal'.", sig)
	}
}
