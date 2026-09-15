package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"time"
)

const (
	firstDefaultPort = 7981
	lastDefaultPort  = 7990
	processHeader    = "X-Servediff-Process"
)

var (
	errNoAvailablePort  = errors.New("no available servediff port")
	errStartupCancelled = errors.New("startup cancelled")
)

type boundServer struct {
	listener   net.Listener
	url        string
	browserURL string
}

type listenFunc func(network, address string) (net.Listener, error)

type processHandler struct {
	next http.Handler
	pid  int
}

func (handler processHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	response.Header().Set(processHeader, strconv.Itoa(handler.pid))
	handler.next.ServeHTTP(response, request)
}

func identifyProcess(next http.Handler) http.Handler {
	return processHandler{next: next, pid: os.Getpid()}
}

func bind(values options, stdin *os.File, stdout io.Writer) (boundServer, error) {
	if values.portSet {
		listener, err := net.Listen("tcp", net.JoinHostPort(values.host, strconv.Itoa(values.port)))
		if err != nil {
			return boundServer{}, fmt.Errorf("listen on %s:%d: %w", values.host, values.port, err)
		}
		return makeBoundServer(values.host, listener), nil
	}
	probe, err := net.Listen("tcp", net.JoinHostPort(values.host, "0"))
	if err != nil {
		return boundServer{}, fmt.Errorf("bind host %s: %w", values.host, err)
	}
	_ = probe.Close()
	listener, err := listenRange(values.host, firstDefaultPort, lastDefaultPort, stdout)
	if err == nil {
		return makeBoundServer(values.host, listener), nil
	}
	if !errors.Is(err, errNoAvailablePort) {
		return boundServer{}, err
	}
	pids, processError := servediffProcesses(values.host)
	if processError != nil {
		return boundServer{}, processError
	}
	confirmed, confirmError := confirmTermination(stdin, stdout)
	if confirmError != nil {
		return boundServer{}, confirmError
	}
	if !confirmed {
		return boundServer{}, errStartupCancelled
	}
	for _, pid := range pids {
		if err := terminateProcess(pid); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return boundServer{}, fmt.Errorf("terminate servediff process %d: %w", pid, err)
		}
	}
	listener, err = waitForRange(values.host, firstDefaultPort, lastDefaultPort, 2*time.Second)
	if err != nil {
		return boundServer{}, fmt.Errorf("ports %d-%d did not become available: %w", firstDefaultPort, lastDefaultPort, err)
	}
	return makeBoundServer(values.host, listener), nil
}

func listenRange(host string, first, last int, stdout io.Writer) (net.Listener, error) {
	return listenRangeWith(host, first, last, stdout, net.Listen)
}

func listenRangeWith(host string, first, last int, stdout io.Writer, listen listenFunc) (net.Listener, error) {
	for port := first; port <= last; port++ {
		listener, err := listen("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
		if err == nil {
			return listener, nil
		}
		if port < last {
			writePortBusy(stdout, port, port+1)
		}
	}
	return nil, errNoAvailablePort
}

func makeBoundServer(host string, listener net.Listener) boundServer {
	port := listener.Addr().(*net.TCPAddr).Port
	url := "http://" + net.JoinHostPort(host, strconv.Itoa(port))
	browserHost := host
	if ip := net.ParseIP(host); ip != nil && ip.IsUnspecified() {
		browserHost = "127.0.0.1"
		if ip.To4() == nil {
			browserHost = "::1"
		}
	}
	return boundServer{
		listener:   listener,
		url:        url,
		browserURL: "http://" + net.JoinHostPort(browserHost, strconv.Itoa(port)),
	}
}

func servediffProcesses(host string) ([]int, error) {
	client := &http.Client{
		Timeout:   300 * time.Millisecond,
		Transport: &http.Transport{},
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	pids := make([]int, 0, lastDefaultPort-firstDefaultPort+1)
	seen := make(map[int]bool)
	for port := firstDefaultPort; port <= lastDefaultPort; port++ {
		url := "http://" + net.JoinHostPort(connectHost(host), strconv.Itoa(port)) + "/openapi.yaml"
		request, err := http.NewRequest(http.MethodHead, url, nil)
		if err != nil {
			return nil, err
		}
		response, err := client.Do(request)
		if err != nil {
			return nil, fmt.Errorf("ports %d-%d are busy; port %d is not a reachable servediff process", firstDefaultPort, lastDefaultPort, port)
		}
		response.Body.Close()
		pid, parseError := strconv.Atoi(response.Header.Get(processHeader))
		if parseError != nil || pid <= 1 || pid == os.Getpid() || seen[pid] {
			return nil, fmt.Errorf("ports %d-%d are busy; port %d is not a distinct servediff process", firstDefaultPort, lastDefaultPort, port)
		}
		seen[pid] = true
		pids = append(pids, pid)
	}
	return pids, nil
}

func connectHost(host string) string {
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsUnspecified() {
		return host
	}
	if ip.To4() != nil {
		return "127.0.0.1"
	}
	return "::1"
}

func confirmTermination(stdin *os.File, stdout io.Writer) (bool, error) {
	input, closeInput, err := promptInput(stdin)
	if err != nil {
		return false, errors.New("10 servediff processes are running on ports 7981-7990; an interactive terminal is required to terminate them")
	}
	defer closeInput()
	color := terminalColors(stdout)
	prompt := "10 servediff processes are running on ports 7981-7990. terminate all and restart on 7981? [y/N] "
	fmt.Fprintf(stdout, "  %s", color.paint(color.yellow, prompt))
	answer, readError := bufio.NewReader(input).ReadString('\n')
	if readError != nil && !errors.Is(readError, io.EOF) {
		return false, readError
	}
	return isYes(answer), nil
}

func promptInput(stdin *os.File) (io.Reader, func(), error) {
	stat, err := stdin.Stat()
	if err == nil && stat.Mode()&os.ModeCharDevice != 0 {
		return stdin, func() {}, nil
	}
	name := "/dev/tty"
	if runtime.GOOS == "windows" {
		name = "CONIN$"
	}
	terminal, err := os.Open(name)
	if err != nil {
		return nil, func() {}, err
	}
	return terminal, func() { _ = terminal.Close() }, nil
}

func waitForRange(host string, first, last int, timeout time.Duration) (net.Listener, error) {
	deadline := time.Now().Add(timeout)
	var lastError error
	for time.Now().Before(deadline) {
		listeners := make([]net.Listener, 0, last-first+1)
		for port := first; port <= last; port++ {
			listener, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
			if err != nil {
				lastError = err
				break
			}
			listeners = append(listeners, listener)
		}
		if len(listeners) == last-first+1 {
			for _, listener := range listeners[1:] {
				_ = listener.Close()
			}
			return listeners[0], nil
		}
		for _, listener := range listeners {
			_ = listener.Close()
		}
		time.Sleep(50 * time.Millisecond)
	}
	return nil, lastError
}
