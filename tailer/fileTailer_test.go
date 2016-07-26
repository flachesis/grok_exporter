package tailer

import (
	"fmt"
	debugLogger "github.com/fstab/grok_exporter/logger"
	"io/ioutil"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

type logrotateOption int
type logrotateMoveOption int
type loggerOption int

const ( // see 'man logrotate'
	_copy         logrotateOption = iota // Don’t change the original logfile at all.
	_copytruncate                        // Truncate the original log file in place instead of removing it.
	_nocreate                            // Don't create a new logfile after rotation.
	_create                              // Create a new empty logfile immediately after rotation.
)

const (
	mv logrotateMoveOption = iota // Move the old logfile to the backup.
	cp                            // Copy the old logfile to the backup, then remove it.
	rm                            // Delete the old logfile without keeping a backup.
)

const (
	closeFileAfterEachLine loggerOption = iota // Logger does not keep the file open.
	keepOpen                                   // Logger keeps the file open.
)

func (opt logrotateOption) String() string {
	switch {
	case opt == _copy:
		return "copy"
	case opt == _copytruncate:
		return "copytruncate"
	case opt == _nocreate:
		return "nocreate"
	case opt == _create:
		return "create"
	default:
		return "unknown"
	}
}

func (opt logrotateMoveOption) String() string {
	switch {
	case opt == mv:
		return "mv"
	case opt == cp:
		return "cp"
	case opt == rm:
		return "rm"
	default:
		return "unknown"
	}
}

func (opt loggerOption) String() string {
	switch {
	case opt == closeFileAfterEachLine:
		return "closeFileAfterEachLine"
	case opt == keepOpen:
		return "keepOpen"
	default:
		return "unknown"
	}
}

func TestFileTailerCloseLogfileAfterEachLine(t *testing.T) {
	testRunNumber := 0
	for _, mvOpt := range []logrotateMoveOption{mv, cp, rm} {
		testRunNumber++
		testLogrotate(t, NewTestRunLogger(testRunNumber), _create, mvOpt, closeFileAfterEachLine)
		testRunNumber++
		testLogrotate(t, NewTestRunLogger(testRunNumber), _nocreate, mvOpt, closeFileAfterEachLine)
	}
	// For logrotate options 'copy' and 'copytruncate', only the mvOpt 'cp' makes sense.
	testRunNumber++
	testLogrotate(t, NewTestRunLogger(testRunNumber), _copy, cp, closeFileAfterEachLine)
	testRunNumber++
	testLogrotate(t, NewTestRunLogger(testRunNumber), _copytruncate, cp, closeFileAfterEachLine)
}

func TestFileTailerKeepLogfileOpen(t *testing.T) {
	// When the logger keeps the file open, only the logrotate options 'copy' and 'copytruncate' make sense.
	testLogrotate(t, NewTestRunLogger(100), _copy, cp, keepOpen)
	testLogrotate(t, NewTestRunLogger(101), _copytruncate, cp, keepOpen)
}

func TestShutdownDuringSyscall(t *testing.T) {
	runTestShutdown(t, "shutdown while the watcher is hanging in the blocking kevent() or syscall.Read() call")
}

func TestShutdownDuringSendEvent(t *testing.T) {
	runTestShutdown(t, "shutdown while the watcher is sending an event")
}

//func TestStress(t *testing.T) {
//	for i := 0; i < 250; i++ {
//		TestFileTailerCloseLogfileAfterEachLine(t)
//		TestFileTailerKeepLogfileOpen(t)
//	}
//}

func testLogrotate(t *testing.T, testRunLogger simpleLogger, logrotateOpt logrotateOption, logrotateMoveOpt logrotateMoveOption, loggerOpt loggerOption) {
	testRunLogger.Debug("Running test with logrotate option '%v', move option '%v', and logger option '%v'.\n", logrotateOpt, logrotateMoveOpt, loggerOpt)
	tmpDir := mkTmpDirOrFail(t)
	defer cleanUp(t, tmpDir)
	logfile := mkTmpFileOrFail(t, tmpDir)
	logger := newLogger(t, logfile, loggerOpt)
	defer logger.close(t)

	logger.debug(t, testRunLogger, "test line 1")
	logger.debug(t, testRunLogger, "test line 2")

	tail := RunFileTailer(logfile, true, testRunLogger)
	defer tail.Close()

	// We don't expect errors. However, start a go-routine listening on
	// the tailer's errorChannel in case something goes wrong.
	go func() {
		for err := range tail.Errors() {
			t.Fatalf("Tailer failed: %v", err.Error())
		}
	}()

	// The first two lines are received without any fsnotify event,
	// because they were written before the watcher was started.
	expect(t, testRunLogger, tail.Lines(), "test line 1", 1*time.Second)
	expect(t, testRunLogger, tail.Lines(), "test line 2", 1*time.Second)

	// Append a line and see if the event is processed.
	logger.debug(t, testRunLogger, "test line 3")
	expect(t, testRunLogger, tail.Lines(), "test line 3", 1*time.Second)

	rotate(t, testRunLogger, logfile, logrotateOpt, logrotateMoveOpt)

	// Log two more lines and see if they are received.
	logger.debug(t, testRunLogger, "line 4")
	expect(t, testRunLogger, tail.Lines(), "line 4", 10*time.Second) // few seconds longer to get filesystem notifications for rotate()
	logger.debug(t, testRunLogger, "line 5")
	expect(t, testRunLogger, tail.Lines(), "line 5", 1*time.Second)
}

func newLogger(t *testing.T, logfile string, opt loggerOption) logger {
	switch {
	case opt == closeFileAfterEachLine:
		return newCloseFileAfterEachLineLogger(t, logfile)
	case opt == keepOpen:
		return newKeepOpenLogger(t, logfile)
	default:
		t.Fatalf("%v: Unsupported logger option.", opt)
		return nil
	}
}

type logger interface {
	debug(t *testing.T, testRunLogger simpleLogger, msg string)
	close(t *testing.T)
}

type closeFileAfterEachLineLogger struct {
	path string
}

func newCloseFileAfterEachLineLogger(t *testing.T, logfile string) logger {
	return &closeFileAfterEachLineLogger{
		path: logfile,
	}
}

func (l *closeFileAfterEachLineLogger) debug(t *testing.T, testRunLogger simpleLogger, msg string) {
	f, err := os.OpenFile(l.path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0644)
	if err != nil {
		t.Fatalf("%v: Failed to open file for writing: %v", l.path, err.Error())
	}
	_, err = f.WriteString(fmt.Sprintf("%v\n", msg))
	if err != nil {
		t.Fatalf("%v: Failed to write to file: %v", l.path, err.Error())
	}
	err = f.Sync()
	if err != nil {
		t.Fatalf("%v: Failed to flush file: %v", l.path, err.Error())
	}
	err = f.Close()
	if err != nil {
		t.Fatalf("%v: Failed to close file: %v", l.path, err.Error())
	}
	testRunLogger.Debug("Wrote log line '%v' with closeFileAfterEachLineLogger.\n", msg)
}

func (l *closeFileAfterEachLineLogger) close(t *testing.T) {
	// nothing to do
}

type keepOpenLogger struct {
	file *os.File
}

func newKeepOpenLogger(t *testing.T, logfile string) logger {
	f, err := os.OpenFile(logfile, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0644)
	if err != nil {
		t.Fatalf("%v: Failed to open file for writing: %v", logfile, err.Error())
	}
	return &keepOpenLogger{
		file: f,
	}
}

func (l *keepOpenLogger) debug(t *testing.T, testRunLogger simpleLogger, msg string) {
	_, err := l.file.WriteString(fmt.Sprintf("%v\n", msg))
	if err != nil {
		t.Fatalf("%v: Failed to write to file: %v", l.file.Name(), err.Error())
	}
	err = l.file.Sync()
	if err != nil {
		t.Fatalf("%v: Failed to flush the file: %v", l.file.Name(), err.Error())
	}
	testRunLogger.Debug("Wrote log line '%v' with keepOpenLogger.\n", msg)
}

func (l *keepOpenLogger) close(t *testing.T) {
	err := l.file.Close()
	if err != nil {
		t.Fatalf("%v: Failed to close logfile: %v", l.file.Name(), err.Error())
	}
}

func mkTmpDirOrFail(t *testing.T) string {
	testdir, err := ioutil.TempDir("", "grok_exporter")
	if err != nil {
		t.Fatalf("Failed to create test directory: %v", err.Error())
	}
	return testdir
}

func cleanUp(t *testing.T, dir string) {
	err := os.RemoveAll(dir)
	if err != nil {
		t.Errorf("%v: Failed to remove the test directory after running the tests: %v", dir, err.Error())
	}
}

func mkTmpFileOrFail(t *testing.T, dir string) string {
	if len(ls(t, dir)) != 0 {
		t.Fatalf("%v: Directory not empty.", dir)
	}
	logfile, err := ioutil.TempFile(dir, "grok_exporter_test_log")
	if err != nil {
		t.Fatalf("Cannot create temp file: %v", err.Error())
	}
	err = logfile.Close()
	if err != nil {
		t.Fatalf("Failed to close temp file: %v", err.Error())
	}
	if len(ls(t, dir)) != 1 {
		t.Fatalf("%v: Expected exactly one file in directory.", dir)
	}
	return logfile.Name()
}

func containsFile(files []os.FileInfo, filename string) bool {
	for _, f := range files {
		if filepath.Base(f.Name()) == filepath.Base(filename) {
			return true
		}
	}
	return false
}

func ls(t *testing.T, path string) []os.FileInfo {
	result, err := ioutil.ReadDir(path)
	if err != nil {
		t.Fatalf("%v: Failed to list directory: %v", path, err.Error())
	}
	return result
}

func rotate(t *testing.T, testRunLogger simpleLogger, logfile string, opt logrotateOption, mvOpt logrotateMoveOption) {
	dir := filepath.Dir(logfile)
	filename := filepath.Base(logfile)
	filesBefore := ls(t, dir)
	if !containsFile(filesBefore, filename) {
		t.Fatalf("%v does not contain %v before logrotate.", dir, filename)
	}
	switch {
	case opt == _nocreate:
		moveOrFail(t, mvOpt, logfile)
	case opt == _create:
		moveOrFail(t, mvOpt, logfile)
		createOrFail(t, logfile)
	case opt == _copytruncate:
		if mvOpt != cp {
			t.Fatalf("Rotating with '%v' does not make sense when moving the logfile with '%v'", opt, mvOpt)
		}
		cpOrFail(t, logfile, fmt.Sprintf("%v.1", logfile))
		truncateOrFail(t, logfile)
	case opt == _copy:
		if mvOpt != cp {
			t.Fatalf("Rotating with '%v' does not make sense when moving the logfile with '%v'", opt, mvOpt)
		}
		cpOrFail(t, logfile, fmt.Sprintf("%v.1", logfile))
	default:
		t.Fatalf("Unknown logrotate option.")
	}
	testRunLogger.Debug("Simulated logrotate with option %v and mvOption %v\n", opt, mvOpt)
}

func moveOrFail(t *testing.T, mvOpt logrotateMoveOption, logfile string) {
	dir := filepath.Dir(logfile)
	filename := filepath.Base(logfile)
	switch {
	case mvOpt == mv:
		mvOrFail(t, logfile, fmt.Sprintf("%v.1", logfile))
	case mvOpt == cp:
		cpOrFail(t, logfile, fmt.Sprintf("%v.1", logfile))
		rmOrFail(t, logfile)
	case mvOpt == rm:
		rmOrFail(t, logfile)
	}
	filesAfterRename := ls(t, dir)
	if containsFile(filesAfterRename, filename) {
		t.Fatalf("%v still contains file %v after mv.", dir, filename)
	}
}

func mvOrFail(t *testing.T, fromPath string, toPath string) {
	err := os.Rename(fromPath, toPath)
	if err != nil {
		t.Fatalf("%v: Failed to mv file: %v", fromPath, err.Error())
	}
}

func cpOrFail(t *testing.T, fromPath string, toPath string) {
	data, err := ioutil.ReadFile(fromPath)
	if err != nil {
		t.Fatalf("%v: Copy failed, cannot read file: %v", fromPath, err.Error())
	}
	err = ioutil.WriteFile(toPath, data, 0644)
	if err != nil {
		t.Fatalf("%v: Copy failed, cannot write file: %v", toPath, err.Error())
	}
}

func rmOrFail(t *testing.T, path string) {
	err := os.Remove(path)
	if err != nil {
		t.Fatalf("%v: Remove failed: %v", path, err.Error())
	}
}

func createOrFail(t *testing.T, logfile string) {
	dir := filepath.Dir(logfile)
	filename := filepath.Base(logfile)
	filesBeforeCreate := ls(t, dir)
	if containsFile(filesBeforeCreate, filename) {
		t.Fatalf("%v contains file %v before create.", dir, filename)
	}
	f, err := os.Create(logfile)
	if err != nil {
		t.Fatalf("Failed to re-create %v while simulating logrotate: %v", filename, err.Error())
	}
	err = f.Close()
	if err != nil {
		t.Fatalf("%v: Failed to close file: %v", filename, err.Error())
	}
	filesAfterCreate := ls(t, dir)
	if !containsFile(filesAfterCreate, filename) {
		t.Fatalf("%v does not contain %v after create.", dir, filename)
	}
}

func truncateOrFail(t *testing.T, logfile string) {
	err := os.Truncate(logfile, 0)
	if err != nil {
		t.Fatalf("%v: Error truncating the file: %v", logfile, err.Error())
	}
}

func expect(t *testing.T, testRunLogger simpleLogger, c chan string, line string, timeout time.Duration) {
	timeoutChan := make(chan bool, 1)
	go func() {
		time.Sleep(timeout)
		timeoutChan <- true
		close(timeoutChan)
	}()
	select {
	case result := <-c:
		if result != line {
			t.Errorf("Expected '%v', but got '%v'.", line, result)
		} else {
			testRunLogger.Debug("Read expected line '%v'\n", line)
		}
	case <-timeoutChan:
		testRunLogger.Debug("Timeout while waiting for line '%v'\n", line)
		t.Errorf("Timeout while waiting for line '%v'", line)
	}
}

type testRunLogger struct {
	stdoutLogger  simpleLogger
	testRunNumber int
}

func NewTestRunLogger(testRunNumber int) *testRunLogger {
	return &testRunLogger{
		stdoutLogger:  debugLogger.New(true),
		testRunNumber: testRunNumber,
	}
}

func (l *testRunLogger) Debug(format string, a ...interface{}) {
	l.stdoutLogger.Debug("[%v] %v", l.testRunNumber, fmt.Sprintf(format, a...))
}

func runTestShutdown(t *testing.T, mode string) {

	if runtime.GOOS == "windows" {
		t.Skip("The shutdown tests are flaky on Windows. We skip them until either golang.org/x/exp/winfsnotify is fixed, or until we do our own implementation. This shouldn't be a problem when running grok_exporter, because in grok_exporter the file system watcher is never stopped.")
	}

	tmpDir := mkTmpDirOrFail(t)
	defer cleanUp(t, tmpDir)

	logfile := mkTmpFileOrFail(t, tmpDir)
	file, err := open(logfile)
	if err != nil {
		t.Fatalf("Cannot create temp file: %v", err.Error())
	}
	defer file.Close()

	lines := make(chan string)
	defer close(lines)

	watcher, err := initWatcher(logfile, file)
	if err != nil {
		t.Error(err)
	}

	eventLoop := startEventLoop(watcher)

	switch {
	case mode == "shutdown while the watcher is hanging in the blocking kevent() or syscall.Read() call":
		time.Sleep(200 * time.Millisecond)
		eventLoop.Close()
	case mode == "shutdown while the watcher is sending an event":
		file.Close()
		err = os.Remove(logfile) // trigger file system event so kevent() or syscall.Read() returns.
		if err != nil {
			t.Errorf("Failed to remove logfile: %v", err)
		}
		// The watcher is now waiting until we read the event from the event channel.
		// However, we shut down and abort the event.
		eventLoop.Close()
	default:
		t.Errorf("Unknown mode: %v", mode)
	}
	_, ok := <-eventLoop.Errors()
	if ok {
		t.Error("error channel not closed")
	}
	_, ok = <-eventLoop.Events()
	if ok {
		t.Error("events channel not closed")
	}
}
