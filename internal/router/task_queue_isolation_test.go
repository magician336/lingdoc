package router

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/alicebob/miniredis/v2"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
)

// The queue identity is what keeps two controlled worlds apart.
//
// asynq v0.26 has no namespace concept, so the ONLY way for two deployments
// on one Redis to serve different queues is a different Redis database index
// for the queue. These tests pin that mechanism where it can break: the
// configuration, the construction path, and the observable consequence (a
// task enqueued in one world is never executed by the other).

func TestTheQueueOwnDatabaseIndexWinsOverTheSharedRedisDatabase(t *testing.T) {
	t.Setenv(AsynqQueueDBEnv, "7")
	t.Setenv("REDIS_DB", "1")
	queue := readAsynqQueueIdentity()
	if queue.DBIndex != 7 || queue.Source != asynqQueueDBSourceQueueEnv {
		t.Fatalf("queue identity = %+v, want database 7 taken from the queue's own setting", queue)
	}
}

func TestTheQueueFallsBackToTheSharedRedisDatabase(t *testing.T) {
	t.Setenv(AsynqQueueDBEnv, "")
	t.Setenv("REDIS_DB", "3")
	queue := readAsynqQueueIdentity()
	if queue.DBIndex != 3 || queue.Source != asynqQueueDBSourceRedisDB {
		t.Fatalf("queue identity = %+v, want database 3 taken from REDIS_DB", queue)
	}
}

func TestAnUnparsableQueueIndexIsNotSilentlyIgnored(t *testing.T) {
	// A typo in the isolation setting must not silently drop the process onto
	// database 0 (which may be a shared queue). It falls back to REDIS_DB and
	// says so.
	t.Setenv(AsynqQueueDBEnv, "not-a-number")
	t.Setenv("REDIS_DB", "2")
	queue := readAsynqQueueIdentity()
	if queue.DBIndex != 2 || queue.Source != asynqQueueDBSourceRedisDB {
		t.Fatalf("queue identity = %+v, want database 2 from REDIS_DB after the unusable value", queue)
	}
}

func TestTheQueueDefaultsToDatabaseZero(t *testing.T) {
	t.Setenv(AsynqQueueDBEnv, "")
	t.Setenv("REDIS_DB", "")
	queue := readAsynqQueueIdentity()
	if queue.DBIndex != 0 || queue.Source != asynqQueueDBSourceDefault {
		t.Fatalf("queue identity = %+v, want database 0 by default", queue)
	}
}

func TestTheOperatorCanSeeWhichQueueTheWorkerServes(t *testing.T) {
	t.Setenv(AsynqQueueDBEnv, "9")
	t.Setenv("REDIS_DB", "0")
	fields := asynqQueueLogFields()
	if !strings.Contains(fields, "queue_redis_db=9") {
		t.Fatalf("log fields = %q, want the queue's database index in them", fields)
	}
	if !strings.Contains(fields, "queue_db_source="+asynqQueueDBSourceQueueEnv) {
		t.Fatalf("log fields = %q, want the source of that index in them", fields)
	}
}

func TestTheQueueRedisClientUsesTheQueueDatabase(t *testing.T) {
	// Everything that inspects the QUEUE -- the task inspector, the runtime
	// task list, the cancel-by-knowledge path -- must look at the QUEUE's
	// database. An inspector left on the application-wide database would
	// report "no tasks" for everything as soon as a deployment isolates its
	// queue, and the cancel path would break SILENTLY.
	t.Setenv(AsynqQueueDBEnv, "6")
	t.Setenv("REDIS_DB", "1")
	client := NewAsynqQueueRedisClient()
	if client.Options().DB != 6 {
		t.Fatalf("the queue redis client is on database %d, want the queue's own 6", client.Options().DB)
	}
}

func TestTheTaskInspectorReadsTheQueueDatabase(t *testing.T) {
	// The inspector is what the runtime task list and the cancel-by-knowledge
	// path read. If it were built on the application-wide Redis database while
	// the queue lives in its own database, it would report "no tasks" for
	// everything -- and nothing would say so.
	t.Setenv(AsynqQueueDBEnv, "4")
	t.Setenv("REDIS_DB", "1")
	queueClient := NewAsynqQueueRedisClient()
	inspector := NewAsynqInspector(queueClient)
	if inspector == nil {
		t.Fatal("no inspector was built")
	}
	taskInspector, ok := NewAsynqTaskInspector(inspector, queueClient).(*asynqTaskInspector)
	if !ok {
		t.Fatal("the task inspector is not the asynq-backed one")
	}
	if taskInspector.redis.Options().DB != 4 {
		t.Fatalf("the task inspector reads database %d, want the queue's own 4",
			taskInspector.redis.Options().DB)
	}
}

func TestTheInspectorSeesTheQueuesOwnTasks(t *testing.T) {
	// asynq keeps the inspector's own fields unexported, so WHICH database it
	// reads cannot be observed directly. This guard is therefore FUNCTIONAL: a
	// task enqueued in the QUEUE's database must be visible to the inspector.
	// An inspector on the application-wide database would report "no tasks"
	// for everything -- the silent failure the queue's own client prevents.
	server := miniredis.RunT(t)
	t.Setenv("REDIS_ADDR", server.Addr())
	t.Setenv("REDIS_PASSWORD", "")
	t.Setenv("REDIS_DB", "1")
	t.Setenv(AsynqQueueDBEnv, "3")

	queueClient := NewAsynqQueueRedisClient()
	inspector := NewAsynqInspector(queueClient)
	client := asynq.NewClientFromRedisClient(queueClient.Client)
	t.Cleanup(func() { _ = client.Close() })

	if _, err := client.Enqueue(
		asynq.NewTask(types.TypeDocumentProcess, []byte(`{"knowledge_id":"probe"}`)),
		asynq.Queue(types.QueueDefault),
	); err != nil {
		t.Fatalf("enqueue into the queue's database: %v", err)
	}
	pending, err := inspector.ListPendingTasks(types.QueueDefault)
	if err != nil {
		t.Fatalf("list pending tasks: %v", err)
	}
	if len(pending) == 0 {
		t.Fatal("the inspector does not see the queue's own tasks; it is looking at another database")
	}
}

// startQueueWorld starts one worker pool through the PRODUCTION construction
// path (newAsynqServer -> getAsynqRedisClientOpt), so the test exercises the
// same code the running service uses, and returns the channel that receives
// every task the pool executed.
func startQueueWorld(t *testing.T, label string) <-chan string {
	t.Helper()
	server := newAsynqServer(1, map[string]int{types.QueueDefault: 1})
	executed := make(chan string, 8)
	mux := asynq.NewServeMux()
	mux.HandleFunc(types.TypeDocumentProcess, func(_ context.Context, task *asynq.Task) error {
		executed <- task.Type()
		return nil
	})
	if err := server.Start(mux); err != nil {
		t.Fatalf("start %s worker: %v", label, err)
	}
	t.Cleanup(server.Shutdown)
	return executed
}

// queueTaskKeys returns the asynq task keys of the default queue in one Redis
// database. asynq namespaces its keys by queue name, so this is the exact set
// of tasks that live in that database's queue.
func queueTaskKeys(t *testing.T, addr string, db int) []string {
	t.Helper()
	client := redis.NewClient(&redis.Options{Addr: addr, DB: db})
	t.Cleanup(func() { _ = client.Close() })
	keys, err := client.Keys(context.Background(), "asynq:{"+types.QueueDefault+"}:t:*").Result()
	if err != nil {
		t.Fatalf("list task keys in database %d: %v", db, err)
	}
	return keys
}

func TestTwoQueueWorldsOnOneRedisNeverConsumeEachOthersTasks(t *testing.T) {
	redisServer := miniredis.RunT(t)
	t.Setenv("REDIS_ADDR", redisServer.Addr())
	t.Setenv("REDIS_USERNAME", "")
	t.Setenv("REDIS_PASSWORD", "")
	t.Setenv("REDIS_DB", "0")

	// World A: its own queue database, and the client that enqueues for it.
	t.Setenv(AsynqQueueDBEnv, "1")
	clientA, err := NewAsyncqClient()
	if err != nil {
		t.Fatalf("world A client: %v", err)
	}
	t.Cleanup(func() { _ = clientA.Close() })

	// World B: a DIFFERENT queue database on the SAME redis.
	t.Setenv(AsynqQueueDBEnv, "2")
	clientB, err := NewAsyncqClient()
	if err != nil {
		t.Fatalf("world B client: %v", err)
	}
	t.Cleanup(func() { _ = clientB.Close() })

	// Phase 1 -- no worker is running yet, so the task's own keys say exactly
	// which queue it landed in. This is the deterministic statement of
	// isolation: the two worlds are two queues, not one.
	const taskA = "queue-isolation-a"
	if _, err := clientA.Enqueue(
		asynq.NewTask(types.TypeDocumentProcess, []byte(`{"knowledge_id":"a"}`)),
		asynq.Queue(types.QueueDefault),
		asynq.TaskID(taskA),
	); err != nil {
		t.Fatalf("enqueue for world A: %v", err)
	}
	if keys := queueTaskKeys(t, redisServer.Addr(), 1); !containsTaskID(keys, taskA) {
		t.Fatalf("database 1 holds %v, want the task enqueued for world A", keys)
	}
	if keys := queueTaskKeys(t, redisServer.Addr(), 2); len(keys) != 0 {
		t.Fatalf("database 2 holds %v, want no task of world A", keys)
	}

	// Phase 2 -- the workers start. World A executes the task that was
	// enqueued for it: only a worker on the SAME database can take a task out
	// of that database's pending list, so this also proves the enqueuing
	// client and the worker pool agree on the queue.
	//
	// The environment is set again before each worker is built, because the
	// queue identity is resolved when the pool is CONSTRUCTED. Setting it once
	// for both worlds would put both pools on one database -- which is the
	// incident this ticket is about.
	t.Setenv(AsynqQueueDBEnv, "1")
	worldA := startQueueWorld(t, "world A")
	t.Setenv(AsynqQueueDBEnv, "2")
	worldB := startQueueWorld(t, "world B")
	select {
	case <-worldA:
	case <-time.After(20 * time.Second):
		t.Fatal("world A did not execute its own task")
	}

	// World B is alive and polling: it executes the task enqueued for it.
	const taskB = "queue-isolation-b"
	if _, err := clientB.Enqueue(
		asynq.NewTask(types.TypeDocumentProcess, []byte(`{"knowledge_id":"b"}`)),
		asynq.Queue(types.QueueDefault),
		asynq.TaskID(taskB),
	); err != nil {
		t.Fatalf("enqueue for world B: %v", err)
	}
	select {
	case <-worldB:
	case <-time.After(20 * time.Second):
		t.Fatal("world B did not execute its own task")
	}

	// Neither world executed the other's task. asynq polls every second by
	// default, so the window covers several poll cycles of a worker that was
	// demonstrably polling.
	select {
	case <-worldB:
		t.Fatal("world B executed a task that was enqueued in world A's queue")
	case <-time.After(3 * time.Second):
	}
	select {
	case <-worldA:
		t.Fatal("world A executed a task that was enqueued in world B's queue")
	case <-time.After(3 * time.Second):
	}
}

func TestTwoQueueWorldsConfiguredOnTheSameDatabaseShareOneQueue(t *testing.T) {
	// The control case: the SAME setting for both worlds puts them on one
	// queue, which is exactly the state the isolation has to make impossible
	// by configuration. It is what makes the isolation test above meaningful:
	// if the queue's own setting were ignored, this is the state every
	// deployment would silently fall into.
	redisServer := miniredis.RunT(t)
	t.Setenv("REDIS_ADDR", redisServer.Addr())
	t.Setenv("REDIS_USERNAME", "")
	t.Setenv("REDIS_PASSWORD", "")
	t.Setenv("REDIS_DB", "0")

	t.Setenv(AsynqQueueDBEnv, "1")
	clientA, err := NewAsyncqClient()
	if err != nil {
		t.Fatalf("shared client: %v", err)
	}
	t.Cleanup(func() { _ = clientA.Close() })

	const taskA = "queue-shared-a"
	if _, err := clientA.Enqueue(
		asynq.NewTask(types.TypeDocumentProcess, []byte(`{"knowledge_id":"a"}`)),
		asynq.Queue(types.QueueDefault),
		asynq.TaskID(taskA),
	); err != nil {
		t.Fatalf("enqueue on the shared queue: %v", err)
	}
	// One queue means one set of task keys: the task is visible to every
	// worker that subscribes to that database.
	if keys := queueTaskKeys(t, redisServer.Addr(), 1); !containsTaskID(keys, taskA) {
		t.Fatalf("database 1 holds %v, want the task on the shared queue", keys)
	}
	if keys := queueTaskKeys(t, redisServer.Addr(), 2); len(keys) != 0 {
		t.Fatalf("database 2 holds %v, want no task", keys)
	}
}

func containsTaskID(keys []string, id string) bool {
	for _, key := range keys {
		if strings.HasSuffix(key, ":"+id) {
			return true
		}
	}
	return false
}
