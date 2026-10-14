package api

// Database routes (PostgreSQL, Redis/Valkey, MongoDB, and tools for every
// engine including MariaDB).
//
//	GET    /v1/accounts/{a}/dbs                          every engine's databases + Redis
//	POST   /v1/accounts/{a}/dbs                          {"engine":"postgres|mongodb","name","note"} → credentials once
//	POST   /v1/accounts/{a}/dbs/sizes                    measure sizes
//	DELETE /v1/accounts/{a}/dbs/{engine}/{db}
//	POST   /v1/accounts/{a}/dbs/{engine}/{db}/password   {"role"} → new password
//	POST   /v1/accounts/{a}/dbs/{engine}/{db}/roles      {"name","access":"read|write"}
//	DELETE /v1/accounts/{a}/dbs/{engine}/{db}/roles/{user}
//	GET    /v1/accounts/{a}/dbs/{engine}/{db}/export     .sql.gz (MongoDB: archive.gz)
//	POST   /v1/accounts/{a}/dbs/{engine}/{db}/import     raw dump, plain or gzipped
//	POST   /v1/accounts/{a}/dbs/{engine}/{db}/diagnose   {"user","password","php"}
//	GET    /v1/accounts/{a}/dbs/processes, POST /v1/accounts/{a}/dbs/processes/{id}/kill?connection=true
//	GET    /v1/accounts/{a}/dbs/slow-queries?sort=duration
//	GET    /v1/accounts/{a}/redis, PUT {"memory_mb","policy","persist","new_password"}, DELETE
//	POST   /v1/accounts/{a}/redis/flush
//
//	GET    /v1/databases                                 engines on this server
//	GET    /v1/databases/processes, POST /v1/databases/processes/{id}/kill
//	GET    /v1/databases/slowlog, PUT {"enabled","long_query_secs","log_queries_not_using_indexes"}
//	GET    /v1/databases/slow-queries?sort=
//	GET    /v1/databases/replication
//	POST   /v1/databases/replication/source              {"replica_ip","server_id"} → replication user
//	POST   /v1/databases/replication/replica             {"primary_host","user","password","server_id","copy_data"} → job
//	POST   /v1/databases/replication/stop                {"promote"}
//	POST   /v1/databases/redis/replicaof                 {"host","port","password"} (empty host stops)

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/YOUR-ORG/ike/agent/internal/databases"
	"github.com/YOUR-ORG/ike/agent/internal/jobs"
	"github.com/YOUR-ORG/ike/agent/internal/kit"
)

func (s *Server) dbRoutes(h func(string, handler)) {
	d := s.F.DB
	ok := func(v any, err error) (int, any, error) { return http.StatusOK, v, err }
	created := func(v any, err error) (int, any, error) { return http.StatusCreated, v, err }
	admin := func(fn handler) handler {
		return func(w http.ResponseWriter, r *http.Request) (int, any, error) {
			if !isAdmin(r) {
				return 0, nil, kit.ErrForbidden
			}
			return fn(w, r)
		}
	}
	eng := func(r *http.Request) string { return r.PathValue("engine") }
	db := func(r *http.Request) string { return r.PathValue("db") }
	base := "/v1/accounts/{a}/dbs"

	h("GET "+base, func(w http.ResponseWriter, r *http.Request) (int, any, error) { return ok(d.List(pathAcct(r))) })
	h("POST "+base, func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var body databases.CreateRequest
		if !decode(w, r, &body) {
			return 0, nil, nil
		}
		return created(d.Create(r.Context(), pathAcct(r), body))
	})
	h("POST "+base+"/sizes", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(d.RefreshSizes(r.Context(), pathAcct(r)))
	})
	h("DELETE "+base+"/{engine}/{db}", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(map[string]string{"deleted": db(r)}, d.Delete(r.Context(), pathAcct(r), eng(r), db(r)))
	})
	h("POST "+base+"/{engine}/{db}/password", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var body struct {
			Role string `json:"role"`
		}
		if r.ContentLength != 0 && !decode(w, r, &body) {
			return 0, nil, nil
		}
		return ok(d.ResetPassword(r.Context(), pathAcct(r), eng(r), db(r), body.Role))
	})
	h("POST "+base+"/{engine}/{db}/roles", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var body struct{ Name, Access string }
		if !decode(w, r, &body) {
			return 0, nil, nil
		}
		return created(d.AddRole(r.Context(), pathAcct(r), eng(r), db(r), body.Name, body.Access))
	})
	h("DELETE "+base+"/{engine}/{db}/roles/{user}", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(map[string]string{"deleted": r.PathValue("user")}, d.RemoveRole(r.Context(), pathAcct(r), eng(r), db(r), r.PathValue("user")))
	})
	h("GET "+base+"/{engine}/{db}/export", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		ext := ".sql.gz"
		if eng(r) == databases.MongoDB {
			ext = ".archive.gz"
		}
		w.Header().Set("Content-Type", "application/gzip")
		w.Header().Set("Content-Disposition", `attachment; filename="`+db(r)+"-"+time.Now().UTC().Format("20060102-1504")+ext+`"`)
		if err := d.Export(r.Context(), pathAcct(r), eng(r), db(r), w); err != nil {
			return 0, nil, err
		}
		return 0, nil, nil
	})
	h("POST "+base+"/{engine}/{db}/import", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 6*time.Hour)
		defer cancel()
		body := http.MaxBytesReader(w, r.Body, 20<<30)
		return ok(map[string]string{"imported": db(r)}, d.Import(ctx, pathAcct(r), eng(r), db(r), body))
	})
	h("POST "+base+"/{engine}/{db}/diagnose", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var body databases.DiagnoseRequest
		if r.ContentLength != 0 && !decode(w, r, &body) {
			return 0, nil, nil
		}
		body.Engine, body.Database = eng(r), db(r)
		return ok(d.Diagnose(r.Context(), pathAcct(r), body))
	})
	processes := func(prefix string, acct func(*http.Request) string, wrap func(handler) handler) {
		h("GET "+prefix+"/processes", wrap(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
			return ok(d.Processes(r.Context(), acct(r)))
		}))
		h("POST "+prefix+"/processes/{id}/kill", wrap(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
			id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
			conn := r.URL.Query().Get("connection") == "true"
			return ok(map[string]int64{"killed": id}, d.Kill(r.Context(), acct(r), id, conn))
		}))
		h("GET "+prefix+"/slow-queries", wrap(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
			limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
			return ok(d.SlowQueries(acct(r), r.URL.Query().Get("sort"), limit))
		}))
	}
	processes(base, pathAcct, func(fn handler) handler { return fn })
	processes("/v1/databases", func(*http.Request) string { return "" }, admin)

	h("GET /v1/accounts/{a}/redis", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(d.RedisStatus(r.Context(), pathAcct(r)))
	})
	h("PUT /v1/accounts/{a}/redis", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var body struct {
			databases.RedisRequest
			NewPassword bool `json:"new_password"`
		}
		if !decode(w, r, &body) {
			return 0, nil, nil
		}
		cred, inst, err := d.SetRedis(r.Context(), pathAcct(r), body.RedisRequest, body.NewPassword)
		return ok(map[string]any{"credentials": cred, "redis": inst}, err)
	})
	h("DELETE /v1/accounts/{a}/redis", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(map[string]string{"removed": "redis"}, d.RemoveRedis(r.Context(), pathAcct(r)))
	})
	h("POST /v1/accounts/{a}/redis/flush", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(map[string]string{"flushed": "redis"}, d.FlushRedis(r.Context(), pathAcct(r)))
	})

	h("GET /v1/databases", func(w http.ResponseWriter, r *http.Request) (int, any, error) { return ok(d.Engines(), nil) })
	h("GET /v1/databases/slowlog", admin(func(w http.ResponseWriter, r *http.Request) (int, any, error) { return ok(d.SlowLog()) }))
	h("PUT /v1/databases/slowlog", admin(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var body databases.SlowLogSettings
		if !decode(w, r, &body) {
			return 0, nil, nil
		}
		return ok(d.SetSlowLog(r.Context(), body))
	}))
	h("GET /v1/databases/replication", admin(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return ok(d.ReplicationStatus(r.Context()))
	}))
	h("POST /v1/databases/replication/source", admin(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var body databases.ReplicationSource
		if !decode(w, r, &body) {
			return 0, nil, nil
		}
		ctx, cancel := long(r)
		defer cancel()
		return ok(d.EnableSource(ctx, body))
	}))
	h("POST /v1/databases/replication/replica", admin(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var body databases.ReplicaRequest
		if !decode(w, r, &body) {
			return 0, nil, nil
		}
		return s.job("db-replica", "Start MariaDB replication from "+body.PrimaryHost, 12*time.Hour, func(ctx context.Context, log jobs.Logger) error {
			return d.StartReplica(ctx, body, log)
		})
	}))
	h("POST /v1/databases/replication/stop", admin(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var body struct {
			Promote bool `json:"promote"`
		}
		if r.ContentLength != 0 && !decode(w, r, &body) {
			return 0, nil, nil
		}
		return ok(map[string]bool{"stopped": true}, d.StopReplica(r.Context(), body.Promote))
	}))
	h("POST /v1/databases/redis/primary", admin(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var body struct {
			ReplicaIP string `json:"replica_ip"`
		}
		if !decode(w, r, &body) {
			return 0, nil, nil
		}
		pw, port, err := d.RedisPrimary(r.Context(), body.ReplicaIP)
		return ok(map[string]any{"password": pw, "port": port}, err)
	}))
	h("POST /v1/databases/redis/replicaof", admin(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var body struct {
			Host     string `json:"host"`
			Port     int    `json:"port"`
			Password string `json:"password"`
		}
		if !decode(w, r, &body) {
			return 0, nil, nil
		}
		return ok(map[string]string{"primary": body.Host}, d.RedisReplicaOf(r.Context(), body.Host, body.Port, body.Password))
	}))
}
