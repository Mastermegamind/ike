package sites

import (
	"context"
	"io"
	"path/filepath"
	"strings"

	"github.com/Mastermegamind/ike/agent/internal/runner"
)

type starterFile struct {
	path string
	body []byte
}

// starterFiles is the sample content a new website starts with.
func (m *Manager) starterFiles(L layout) []starterFile {
	switch L.Runtime {
	case RuntimePHP:
		return []starterFile{{filepath.Join(L.DocRoot, "index.php"), m.starterPage("index.php", L.Domain)}}
	case RuntimeStatic:
		return []starterFile{{filepath.Join(L.DocRoot, "index.html"), m.starterPage("index.html", L.Domain)}}
	case RuntimeNode:
		return []starterFile{{filepath.Join(L.AppDir, "server.js"), []byte(sampleNodeApp)}}
	case RuntimePython:
		return []starterFile{{filepath.Join(L.AppDir, "app.py"), []byte(samplePythonApp)}}
	case RuntimeJava:
		return []starterFile{{filepath.Join(L.AppDir, "Main.java"), []byte(sampleJavaApp)}}
	case RuntimeGo:
		return []starterFile{
			{filepath.Join(L.AppDir, "go.mod"), []byte("module app\n\ngo 1.22\n")},
			{filepath.Join(L.AppDir, "main.go"), []byte(sampleGoApp)},
		}
	case RuntimeDart:
		return []starterFile{
			{filepath.Join(L.AppDir, "pubspec.yaml"), []byte("name: app\nenvironment:\n  sdk: ^3.0.0\n")},
			{filepath.Join(L.AppDir, "bin", "server.dart"), []byte(sampleDartApp)},
		}
	}
	return nil
}

// runtimeHome is the folder above a runtime's bin directory (JAVA_HOME, GOROOT).
func runtimeHome(bin string) string { return filepath.Dir(bin) }

// runtimeEnv lists the extra variables an app's process needs, beyond PORT,
// HOST and PATH. Values are paths Ike chose, so they never contain quotes.
func (m *Manager) runtimeEnv(L layout) []string {
	switch L.Runtime {
	case RuntimeNode:
		return []string{`NODE_ENV="${NODE_ENV:-production}"`}
	case RuntimePython:
		return []string{`VIRTUAL_ENV="` + L.AppDir + `/.venv"`, "PYTHONUNBUFFERED=1"}
	case RuntimeJava:
		return []string{`JAVA_HOME="` + runtimeHome(L.RuntimeBin) + `"`}
	case RuntimeGo:
		return []string{`GOROOT="` + runtimeHome(L.RuntimeBin) + `"`, `GOPATH="` + L.Home + `/go"`,
			`GOCACHE="` + L.Home + `/.cache/go-build"`, "GOTOOLCHAIN=local"}
	case RuntimeDart:
		return []string{`PUB_CACHE="` + L.Home + `/.pub-cache"`}
	}
	return nil
}

// runtimePath is the PATH an app (or its build) runs with.
func runtimePath(L layout, dir string) string {
	parts := []string{}
	switch L.Runtime {
	case RuntimeNode:
		parts = append(parts, dir+"/node_modules/.bin")
	case RuntimePython:
		parts = append(parts, dir+"/.venv/bin")
	case RuntimeGo:
		parts = append(parts, L.Home+"/go/bin")
	case RuntimeDart:
		parts = append(parts, L.Home+"/.pub-cache/bin")
	}
	if L.RuntimeBin != "" {
		parts = append(parts, L.RuntimeBin)
	}
	return strings.Join(append(parts, "/usr/local/bin", "/usr/bin", "/bin"), ":")
}

// userEnv is a clean environment for commands run as the account user: no
// variables leak from the root agent.
func (m *Manager) userEnv(L layout, dir string, extra ...string) []string {
	env := []string{
		"HOME=" + L.Home, "USER=" + L.User, "LOGNAME=" + L.User, "LANG=C.UTF-8",
		"PATH=" + runtimePath(L, dir), "TMPDIR=" + L.TmpDir,
	}
	for _, e := range m.runtimeEnv(L) {
		k, v, _ := strings.Cut(e, "=")
		v = strings.Trim(v, `"`)
		if strings.HasPrefix(v, "${") { // shell default syntax, used only by the start script
			v = "production"
		}
		if k == "VIRTUAL_ENV" {
			v = dir + "/.venv"
		}
		env = append(env, k+"="+v)
	}
	return append(env, extra...)
}

// asUser runs a program as the account user with a clean environment.
func (m *Manager) asUser(ctx context.Context, L layout, dir string, out io.Writer, env []string, name string, args ...string) ([]byte, error) {
	if env == nil {
		env = m.userEnv(L, dir)
	}
	return m.run.Exec(ctx, runner.Command{
		Name: "runuser", Args: append([]string{"-u", L.User, "--", name}, args...),
		Env: env, Dir: m.p(dir), Output: out,
	})
}

// prepareApp does the one-time setup a new app needs before it can start.
func (m *Manager) prepareApp(ctx context.Context, L layout) error {
	switch L.Runtime {
	case RuntimePython:
		// A private virtualenv, created as the account user, so pip installs stay in the app.
		_, err := m.run.Run(ctx, "runuser", "-u", L.User, "--",
			filepath.Join(L.RuntimeBin, "python3"), "-m", "venv", filepath.Join(L.AppDir, ".venv"))
		return err
	case RuntimeGo:
		// The sample is compiled so the app starts right away.
		_, err := m.asUser(ctx, L, L.AppDir, nil, nil, filepath.Join(L.RuntimeBin, "go"), "build", "-o", "app", ".")
		return err
	}
	return nil
}

const sampleJavaApp = `// Sample app created by Ike. Java 11+ runs this single file directly.
// For a real project, build a jar (mvn package or ./gradlew build) and set the
// start command to: java -jar target/app.jar
import com.sun.net.httpserver.HttpServer;
import java.net.InetSocketAddress;
import java.nio.charset.StandardCharsets;

public class Main {
    public static void main(String[] args) throws Exception {
        int port = Integer.parseInt(System.getenv().getOrDefault("PORT", "8080"));
        HttpServer server = HttpServer.create(new InetSocketAddress("127.0.0.1", port), 0);
        server.createContext("/", exchange -> {
            byte[] body = ("<h1>Your Java app is running</h1><p>Java " + System.getProperty("java.version") + "</p>")
                .getBytes(StandardCharsets.UTF_8);
            exchange.getResponseHeaders().set("Content-Type", "text/html; charset=utf-8");
            exchange.sendResponseHeaders(200, body.length);
            exchange.getResponseBody().write(body);
            exchange.close();
        });
        System.out.println("Listening on port " + port);
        server.start();
    }
}
`

const sampleGoApp = `// Sample app created by Ike. Build with: go build -o app .
// then restart the app. Deployments from Git build it for you.
package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"runtime"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, "<h1>Your Go app is running</h1><p>%s</p>", runtime.Version())
	})
	log.Printf("Listening on port %s", port)
	log.Fatal(http.ListenAndServe("127.0.0.1:"+port, nil))
}
`

const sampleDartApp = `// Sample app created by Ike. Replace it with your own (shelf, dart_frog ...),
// then restart the app.
import 'dart:io';

Future<void> main() async {
  final port = int.parse(Platform.environment['PORT'] ?? '8080');
  final server = await HttpServer.bind(InternetAddress.loopbackIPv4, port);
  print('Listening on port $port');
  await for (final request in server) {
    request.response
      ..headers.contentType = ContentType.html
      ..write('<h1>Your Dart app is running</h1><p>Dart ${Platform.version.split(' ').first}</p>')
      ..close();
  }
}
`
