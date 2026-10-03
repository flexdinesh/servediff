package diffsource

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Assigning a job after Start lets Git spawn children before ownership exists.
// JOB_LIST makes ownership part of CreateProcess; the job cannot be escaped.
const procThreadAttributeJobList = 0x0002000d

func runGitCommand(ctx context.Context, command *exec.Cmd) (result error) {
	if command.Err != nil {
		return command.Err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return err
	}
	jobOpen := true
	closeJob := func() error {
		if !jobOpen {
			return nil
		}
		err := windows.CloseHandle(job)
		if err == nil {
			jobOpen = false
		}
		return err
	}
	defer func() { result = errors.Join(result, closeJob()) }()
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		return err
	}
	input, err := os.Open(os.DevNull)
	if err != nil {
		return err
	}
	defer input.Close()
	stdout, stdoutWriter, err := os.Pipe()
	if err != nil {
		return err
	}
	defer stdout.Close()
	defer stdoutWriter.Close()
	stderr, stderrWriter, err := os.Pipe()
	if err != nil {
		return err
	}
	defer stderr.Close()
	defer stderrWriter.Close()
	process, err := startGitInJob(command, job, input, stdoutWriter, stderrWriter)
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, windows.CloseHandle(process.Process)) }()
	stopTree := func() error {
		if !jobOpen {
			return nil
		}
		terminateErr := windows.TerminateJobObject(job, 1)
		closeErr := closeJob()
		if terminateErr != nil || closeErr != nil {
			return errors.Join(terminateErr, closeErr, windows.TerminateProcess(process.Process, 1))
		}
		return nil
	}
	result = windows.CloseHandle(process.Thread)
	command.Process, err = os.FindProcess(int(process.ProcessId))
	if err != nil {
		cleanupErr := stopTree()
		event, waitErr := windows.WaitForSingleObject(process.Process, 1000)
		if event == uint32(windows.WAIT_TIMEOUT) {
			waitErr = errors.Join(waitErr, errors.New("timed out reaping Git process"))
		}
		return errors.Join(result, err, cleanupErr, waitErr)
	}
	defer command.Process.Release()
	// Only child handles may retain the write ends while output is draining.
	result = errors.Join(result, stdoutWriter.Close(), stderrWriter.Close())
	drained := make(chan error, 2)
	for _, stream := range []struct {
		reader *os.File
		writer io.Writer
	}{{stdout, command.Stdout}, {stderr, command.Stderr}} {
		go func() {
			defer stream.reader.Close()
			writer := stream.writer
			if writer == nil {
				writer = io.Discard
			}
			_, err := io.Copy(writer, stream.reader)
			drained <- err
		}()
	}
	// Reaping and draining share one cleanup budget. Finite native waits avoid
	// a stranded waiter goroutine if termination itself fails.
	var cleanupDeadline time.Time
	if result != nil {
		cleanupDeadline = time.Now().Add(time.Second)
		result = errors.Join(result, stopTree())
	}
	leaderExited := false
	remaining := 2
	for {
	drainAvailable:
		for remaining > 0 {
			select {
			case copyErr := <-drained:
				remaining--
				result = errors.Join(result, copyErr)
				if copyErr != nil && cleanupDeadline.IsZero() {
					cleanupDeadline = time.Now().Add(time.Second)
					result = errors.Join(result, stopTree())
				}
			default:
				break drainAvailable
			}
		}
		if ctx.Err() != nil && cleanupDeadline.IsZero() {
			cleanupDeadline = time.Now().Add(time.Second)
			result = errors.Join(result, ctx.Err(), stopTree())
		}
		if !cleanupDeadline.IsZero() && !time.Now().Before(cleanupDeadline) {
			result = errors.Join(result, errors.New("timed out reaping Git process"))
			break
		}
		event, waitErr := windows.WaitForSingleObject(process.Process, 20)
		if waitErr != nil {
			result = errors.Join(result, waitErr)
			if cleanupDeadline.IsZero() {
				cleanupDeadline = time.Now().Add(time.Second)
				result = errors.Join(result, stopTree())
			}
		} else if event == windows.WAIT_OBJECT_0 {
			leaderExited = true
			break
		}
	}
	if leaderExited {
		state, waitErr := command.Process.Wait()
		result = errors.Join(result, waitErr)
		command.ProcessState = state
		if state != nil && !state.Success() {
			result = errors.Join(result, &exec.ExitError{ProcessState: state})
		}
	}
	if cleanupDeadline.IsZero() {
		cleanupDeadline = time.Now().Add(time.Second)
	}
	timer := time.NewTimer(time.Until(cleanupDeadline))
	defer timer.Stop()
	for remaining > 0 {
		select {
		case err := <-drained:
			remaining--
			result = errors.Join(result, err)
		case <-ctx.Done():
			result = errors.Join(result, ctx.Err(), stopTree())
			// This case must not starve the drain deadline once cancelled.
			ctx = context.WithoutCancel(ctx)
		case <-timer.C:
			result = errors.Join(result, exec.ErrWaitDelay, stopTree())
			_ = stdout.Close()
			_ = stderr.Close()
			for range remaining {
				result = errors.Join(result, <-drained)
			}
			return result
		}
	}
	// Kill helpers that closed output but outlived the Git leader, too.
	return errors.Join(result, stopTree())
}

func startGitInJob(command *exec.Cmd, job windows.Handle, files ...*os.File) (*windows.ProcessInformation, error) {
	attributes, err := windows.NewProcThreadAttributeList(2)
	if err != nil {
		return nil, err
	}
	defer attributes.Delete()
	handles := make([]windows.Handle, len(files))
	for index, file := range files {
		if err := windows.DuplicateHandle(windows.CurrentProcess(), windows.Handle(file.Fd()), windows.CurrentProcess(), &handles[index], 0, true, windows.DUPLICATE_SAME_ACCESS); err != nil {
			return nil, err
		}
		defer windows.CloseHandle(handles[index])
	}
	if err := attributes.Update(windows.PROC_THREAD_ATTRIBUTE_HANDLE_LIST, unsafe.Pointer(&handles[0]), uintptr(len(handles))*unsafe.Sizeof(handles[0])); err != nil {
		return nil, err
	}
	if err := attributes.Update(procThreadAttributeJobList, unsafe.Pointer(&job), unsafe.Sizeof(job)); err != nil {
		return nil, err
	}
	path, err := filepath.Abs(command.Path)
	if err != nil {
		return nil, err
	}
	application, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	arguments, err := windows.UTF16PtrFromString(windows.ComposeCommandLine(command.Args))
	if err != nil {
		return nil, err
	}
	var directory *uint16
	if command.Dir != "" {
		directory, err = windows.UTF16PtrFromString(command.Dir)
		if err != nil {
			return nil, err
		}
	}
	environment := command.Environ()
	for _, entry := range environment {
		if strings.ContainsRune(entry, 0) {
			return nil, errors.New("invalid NUL in Git environment")
		}
	}
	sort.Slice(environment, func(first, second int) bool {
		return strings.ToUpper(environment[first]) < strings.ToUpper(environment[second])
	})
	environmentBlock := utf16.Encode([]rune(strings.Join(environment, "\x00") + "\x00\x00"))
	startup := windows.StartupInfoEx{}
	startup.Cb = uint32(unsafe.Sizeof(startup))
	startup.Flags = windows.STARTF_USESTDHANDLES
	startup.StdInput, startup.StdOutput, startup.StdErr = handles[0], handles[1], handles[2]
	startup.ProcThreadAttributeList = attributes.List()
	var process windows.ProcessInformation
	if err := windows.CreateProcess(application, arguments, nil, nil, true, windows.CREATE_UNICODE_ENVIRONMENT|windows.EXTENDED_STARTUPINFO_PRESENT, &environmentBlock[0], directory, &startup.StartupInfo, &process); err != nil {
		return nil, err
	}
	return &process, nil
}
