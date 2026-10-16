package ops

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Dnchua/mercd-server/server/internal/httpapi"
)

func newOpsServer(t *testing.T, dep OpsDeps) (*httptest.Server, *httpapi.Registry) {
	t.Helper()
	reg := httpapi.NewRegistry()
	dep.Registry = reg
	RegisterOps(reg, dep)
	reqLog := filepath.Join(t.TempDir(), "requests.ndjson")
	srv, err := httpapi.NewServer(httpapi.Options{RequestLog: reqLog}, reg, slog.Default())
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, reg
}

func TestOpsHealth(t *testing.T) {
	ts, _ := newOpsServer(t, OpsDeps{
		Started:    time.Now(),
		AssetsDir:  "assets/cn350",
		StateDir:   "server-state-cn35",
		ServerHost: "127.0.0.1:18091",
		Accounts:   func() (int, error) { return 7, nil },
		Log:        slog.New(slog.NewTextHandler(&strings.Builder{}, nil)),
	})

	resp, err := ts.Client().Get(ts.URL + httpapi.OpsPrefix + "health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("health 状态 = %d，期望 200", resp.StatusCode)
	}
	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("解析 health 响应: %v", err)
	}
	if payload["status"] != "ok" || payload["service"] != "mercd35" || payload["protocol"] != "cn35-3.5.0" {
		t.Fatalf("health 关键字段 = %v", payload)
	}
	if payload["accounts"] != float64(7) {
		t.Fatalf("accounts=%v，期望 7", payload["accounts"])
	}
	if payload["server_host"] != "127.0.0.1:18091" {
		t.Fatalf("server_host=%v", payload["server_host"])
	}
	if _, ok := payload["routes_total"]; !ok {
		t.Fatal("health 缺少 routes_total")
	}
}

func TestOpsHealthNilAndErrorAccounts(t *testing.T) {
	ts, _ := newOpsServer(t, OpsDeps{Started: time.Now()})
	resp, err := ts.Client().Get(ts.URL + httpapi.OpsPrefix + "health")
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&payload)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || payload["accounts"] != float64(0) {
		t.Fatalf("nil Accounts 时 status=%d accounts=%v", resp.StatusCode, payload["accounts"])
	}

	ts2, _ := newOpsServer(t, OpsDeps{
		Started:  time.Now(),
		Accounts: func() (int, error) { return 0, errors.New("db down") },
	})
	resp, err = ts2.Client().Get(ts2.URL + httpapi.OpsPrefix + "health")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("Accounts 出错时状态 = %d，期望 500", resp.StatusCode)
	}
}

func TestOpsRoutesAndUnknownEndpoints(t *testing.T) {
	ts, reg := newOpsServer(t, OpsDeps{Started: time.Now()})

	resp, err := ts.Client().Get(ts.URL + httpapi.OpsPrefix + "routes")
	if err != nil {
		t.Fatal(err)
	}
	var routesPayload struct {
		Routes []json.RawMessage `json:"routes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&routesPayload); err != nil {
		t.Fatalf("解析 routes 响应: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || len(routesPayload.Routes) != reg.Len() {
		t.Fatalf("routes 状态=%d 条数=%d，期望 200/%d", resp.StatusCode, len(routesPayload.Routes), reg.Len())
	}

	// 撞两条未注册路由，再读 backlog 聚合端点。
	client := ts.Client()
	for i := 0; i < 2; i++ {
		resp, err := client.Get(ts.URL + "/home/index")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotImplemented {
			t.Fatalf("/home/index 状态 = %d，期望 501", resp.StatusCode)
		}
	}
	resp, err = client.Get(ts.URL + httpapi.OpsPrefix + "unknown-endpoints")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unknown-endpoints 状态 = %d，期望 200", resp.StatusCode)
	}
	var payload struct {
		UnknownEndpoints  []httpapi.UnknownEndpointStat `json:"unknown_endpoints"`
		DistinctEndpoints int                           `json:"distinct_endpoints"`
		Total501Hits      int                           `json:"total_501_hits"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("解析 unknown-endpoints 响应: %v", err)
	}
	if payload.Total501Hits != 2 || payload.DistinctEndpoints != 1 {
		t.Fatalf("聚合统计 = total:%d distinct:%d，期望 2/1",
			payload.Total501Hits, payload.DistinctEndpoints)
	}
	first := payload.UnknownEndpoints[0]
	if first.Method != http.MethodGet || first.Path != "/home/index" || first.Count != 2 {
		t.Fatalf("榜首 = {%s %s count=%d}，期望 {GET /home/index count=2}",
			first.Method, first.Path, first.Count)
	}
	for _, stat := range payload.UnknownEndpoints {
		if strings.HasPrefix(stat.Path, httpapi.OpsPrefix) {
			t.Errorf("运维端点自身不得进入 backlog: %#v", stat)
		}
	}
}
