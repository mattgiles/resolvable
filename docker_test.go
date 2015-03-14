package main

import (
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	"github.com/mgood/resolve/dockerpool"

	dockerapi "github.com/fsouza/go-dockerclient"
)

var useNativeContainers = os.Getenv("DESTROY_NATIVE_CONTAINERS") != ""

var DaemonPool dockerpool.Pool

func TestMain(m *testing.M) {
	os.Exit(runMain(m))
}

func runMain(m *testing.M) int {
	if err := setup(); err != nil {
		log.Fatal(err)
	}
	defer DaemonPool.Close()

	return m.Run()
}


func setup() error {
	var pool dockerpool.Pool
	var err error

	if useNativeContainers {
		pool, err = dockerpool.NewNativePool("gliderlabs/alpine:latest")
	} else {
		pool, err = dockerpool.NewDockerInDockerPool("gliderlabs/alpine:latest")
	}

	if err != nil {
		return err
	}

	DaemonPool = pool
	return nil
}

func TestStartupShutdown(t *testing.T) {
	if useNativeContainers {
		t.Skip("not supported with native containers, cannot shutdown the native Docker daemon")
	}
	t.Parallel()

	daemon, err := dockerpool.NewDockerInDockerDaemon()
	ok(t, err)
	defer daemon.Close()

	dns := RunDebugResolver(daemon.Client)

	assertNext(t, "listen", dns.ch, 10*time.Second)

	ok(t, daemon.Close())
	assertNext(t, "close", dns.ch, 20*time.Second)
}

func TestAddContainerBeforeStarted(t *testing.T) {
	t.Parallel()

	daemon, err := DaemonPool.Borrow()
	ok(t, err)
	defer DaemonPool.Return(daemon)

	containerId, err := daemon.RunSimple("sleep", "30")
	ok(t, err)

	dns := RunDebugResolver(daemon.Client)

	assertNext(t, "add: "+containerId, dns.ch, time.Second)
	assertNext(t, "add: bridge:docker0", dns.ch, time.Second)
	assertNext(t, "listen", dns.ch, 10*time.Second)
}

func TestAddRemoveWhileRunning(t *testing.T) {
	t.Parallel()

	daemon, err := DaemonPool.Borrow()
	ok(t, err)
	defer DaemonPool.Return(daemon)

	dns := RunDebugResolver(daemon.Client)

	assertNext(t, "listen", dns.ch, 10*time.Second)

	containerId, err := daemon.RunSimple("sleep", "30")
	ok(t, err)

	assertNext(t, "add: "+containerId, dns.ch, time.Second)
	assertNext(t, "add: bridge:docker0", dns.ch, time.Second)

	ok(t, daemon.Client.KillContainer(dockerapi.KillContainerOptions{
		ID: containerId,
	}))

	assertNext(t, "remove: "+containerId, dns.ch, time.Second)
}

func TestAddUpstreamDefaultPort(t *testing.T) {
	t.Parallel()

	daemon, err := DaemonPool.Borrow()
	ok(t, err)
	defer DaemonPool.Return(daemon)

	dns := RunDebugResolver(daemon.Client)

	assertNext(t, "listen", dns.ch, 10*time.Second)

	containerId, err := daemon.Run(dockerapi.CreateContainerOptions{
		Config: &dockerapi.Config{
			Image: "gliderlabs/alpine",
			Cmd:   []string{"sleep", "30"},
			Env:   []string{"DNS_RESOLVES=domain"},
		},
	}, nil)
	ok(t, err)

	container, err := daemon.Client.InspectContainer(containerId)
	ok(t, err)

	assertNext(t, "add: "+containerId, dns.ch, time.Second)
	assertNext(t,
		fmt.Sprintf("add upstream: %v %v %v [domain]", containerId, container.NetworkSettings.IPAddress, 53),
		dns.ch, time.Second,
	)
	assertNext(t, "add: bridge:docker0", dns.ch, time.Second)

	ok(t, daemon.Client.KillContainer(dockerapi.KillContainerOptions{
		ID: containerId,
	}))

	assertNext(t, "remove: "+containerId, dns.ch, time.Second)
	assertNext(t, "remove upstream: "+containerId, dns.ch, time.Second)
}

func TestAddUpstreamEmptyDomains(t *testing.T) {
	t.Parallel()

	daemon, err := DaemonPool.Borrow()
	ok(t, err)
	defer DaemonPool.Return(daemon)

	dns := RunDebugResolver(daemon.Client)

	assertNext(t, "listen", dns.ch, 10*time.Second)

	containerId, err := daemon.Run(dockerapi.CreateContainerOptions{
		Config: &dockerapi.Config{
			Image: "gliderlabs/alpine",
			Cmd:   []string{"sleep", "30"},
			Env:   []string{"DNS_RESOLVES="},
		},
	}, nil)
	ok(t, err)

	assertNext(t, "add: "+containerId, dns.ch, time.Second)
	select {
	case msg := <-dns.ch:
		t.Fatalf("expected no more results, got: %v", msg)
	case <-time.After(time.Second):
	}
}

func TestAddUpstreamEmptyPort(t *testing.T) {
	t.Parallel()

	daemon, err := DaemonPool.Borrow()
	ok(t, err)
	defer DaemonPool.Return(daemon)

	dns := RunDebugResolver(daemon.Client)

	assertNext(t, "listen", dns.ch, 10*time.Second)

	containerId, err := daemon.Run(dockerapi.CreateContainerOptions{
		Config: &dockerapi.Config{
			Image: "gliderlabs/alpine",
			Cmd:   []string{"sleep", "30"},
			Env: []string{
				"DNS_RESOLVES=domain",
				"DNS_PORT=",
			},
		},
	}, nil)
	ok(t, err)

	container, err := daemon.Client.InspectContainer(containerId)
	ok(t, err)

	assertNext(t, "add: "+containerId, dns.ch, time.Second)
	assertNext(t,
		fmt.Sprintf("add upstream: %v %v %v [domain]", containerId, container.NetworkSettings.IPAddress, 53),
		dns.ch, time.Second,
	)
}

func TestAddUpstreamAlternatePort(t *testing.T) {
	t.Parallel()

	daemon, err := DaemonPool.Borrow()
	ok(t, err)
	defer DaemonPool.Return(daemon)

	dns := RunDebugResolver(daemon.Client)

	assertNext(t, "listen", dns.ch, 10*time.Second)

	containerId, err := daemon.Run(dockerapi.CreateContainerOptions{
		Config: &dockerapi.Config{
			Image: "gliderlabs/alpine",
			Cmd:   []string{"sleep", "30"},
			Env: []string{
				"DNS_RESOLVES=domain",
				"DNS_PORT=5353",
			},
		},
	}, nil)
	ok(t, err)

	container, err := daemon.Client.InspectContainer(containerId)
	ok(t, err)

	assertNext(t, "add: "+containerId, dns.ch, time.Second)
	assertNext(t,
		fmt.Sprintf("add upstream: %v %v %v [domain]", containerId, container.NetworkSettings.IPAddress, 5353),
		dns.ch, time.Second,
	)
}

func TestAddUpstreamInvalidPort(t *testing.T) {
	t.Parallel()

	daemon, err := DaemonPool.Borrow()
	ok(t, err)
	defer DaemonPool.Return(daemon)

	dns := RunDebugResolver(daemon.Client)

	assertNext(t, "listen", dns.ch, 10*time.Second)

	containerId, err := daemon.Run(dockerapi.CreateContainerOptions{
		Config: &dockerapi.Config{
			Image: "gliderlabs/alpine",
			Cmd:   []string{"sleep", "30"},
			Env: []string{
				"DNS_RESOLVES=domain",
				"DNS_PORT=invalid",
			},
		},
	}, nil)
	ok(t, err)

	assertNext(t, "add: "+containerId, dns.ch, time.Second)
	// XXX should it still attempt to add the bridge if there is another error?
	// assertNext(t, "add: bridge:docker0", dns.ch, time.Second)

	select {
	case msg := <-dns.ch:
		t.Fatalf("expected no more results, got: %v", msg)
	default:
	}
}

func TestAddUpstreamDomains(t *testing.T) {
	t.Parallel()

	daemon, err := DaemonPool.Borrow()
	ok(t, err)
	defer DaemonPool.Return(daemon)

	dns := RunDebugResolver(daemon.Client)

	assertNext(t, "listen", dns.ch, 10*time.Second)

	containerId, err := daemon.Run(dockerapi.CreateContainerOptions{
		Config: &dockerapi.Config{
			Image: "gliderlabs/alpine",
			Cmd:   []string{"sleep", "30"},
			Env: []string{
				"DNS_RESOLVES=domain,another.domain",
				"DNS_PORT=5353",
			},
		},
	}, nil)
	ok(t, err)

	container, err := daemon.Client.InspectContainer(containerId)
	ok(t, err)

	assertNext(t, "add: "+containerId, dns.ch, time.Second)
	assertNext(t,
		fmt.Sprintf("add upstream: %v %v %v [domain another.domain]", containerId, container.NetworkSettings.IPAddress, 5353),
		dns.ch, time.Second,
	)
}

func assertNext(tb testing.TB, expected string, ch chan string, timeout time.Duration) {
	select {
	case actual := <-ch:
		equals(tb, expected, actual)
	case <-time.After(timeout):
		_, file, line, _ := runtime.Caller(1)
		fmt.Printf("\033[31m%s:%d: timed out after %v, exp: %s\033[39m\n\n", filepath.Base(file), line, timeout, expected)
		tb.FailNow()
	}
}

// TODO add a test for when the container doesn't start up right,
// the IP will be nil, since the container aborted, so we shouldn't try to add it at all

////////////////////////////////////////////////////////////////////////////////
// Helpers
////////////////////////////////////////////////////////////////////////////////

// ok fails the test if an err is not nil.
func ok(tb testing.TB, err error) {
	if err != nil {
		_, file, line, _ := runtime.Caller(1)
		fmt.Printf("\033[31m%s:%d: unexpected error: %s\033[39m\n\n", filepath.Base(file), line, err.Error())
		tb.FailNow()
	}
}

// equals fails the test if exp is not equal to act.
func equals(tb testing.TB, exp, act interface{}) {
	if !reflect.DeepEqual(exp, act) {
		_, file, line, _ := runtime.Caller(1)
		fmt.Printf("\033[31m%s:%d:\n\n\texp: %#v\n\n\tgot: %#v\033[39m\n\n", filepath.Base(file), line, exp, act)
		tb.FailNow()
	}
}

type DebugResolver struct {
	ch chan string
}

func RunDebugResolver(client *dockerapi.Client) *DebugResolver {
	dns := &DebugResolver{make(chan string)}
	go registerContainers(client, dns, "docker")
	return dns
}

func (r *DebugResolver) AddHost(id string, addr net.IP, name string, aliases ...string) error {
	// r.ch <- fmt.Sprintf("add: %v %v %v %v", id, addr, name, aliases)
	r.ch <- fmt.Sprintf("add: %v", id)
	return nil
}

func (r *DebugResolver) RemoveHost(id string) error {
	r.ch <- fmt.Sprintf("remove: %v", id)
	return nil
}

func (r *DebugResolver) AddUpstream(id string, addr net.IP, port int, domains ...string) error {
	r.ch <- fmt.Sprintf("add upstream: %v %v %v %v", id, addr, port, domains)
	return nil
}

func (r *DebugResolver) RemoveUpstream(id string) error {
	r.ch <- fmt.Sprintf("remove upstream: %v", id)
	return nil
}

func (r *DebugResolver) Listen() error {
	r.ch <- "listen"
	return nil
}

func (r *DebugResolver) Close() {
	r.ch <- "close"
}
