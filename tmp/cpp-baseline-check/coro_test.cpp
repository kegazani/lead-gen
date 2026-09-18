// C++20 coroutine smoke test: promise_type + custom awaitable + co_await/co_return
#include <coroutine>
#include <cstdio>
#include <exception>

struct Awaitable {
    bool await_ready() const noexcept { return false; }
    void await_suspend(std::coroutine_handle<>) const noexcept {}
    int  await_resume() const noexcept { return 42; }
};

struct Task {
    struct promise_type {
        int value = 0;
        Task get_return_object() noexcept {
            return Task{std::coroutine_handle<promise_type>::from_promise(*this)};
        }
        std::suspend_never  initial_suspend() noexcept { return {}; }
        std::suspend_always final_suspend()   noexcept { return {}; }
        void return_value(int v) noexcept { value = v; }
        void unhandled_exception() { std::terminate(); }
    };
    std::coroutine_handle<promise_type> h{};
    explicit Task(std::coroutine_handle<promise_type> hh) noexcept : h(hh) {}
    Task(Task&& o) noexcept : h(o.h) { o.h = {}; }
    Task(const Task&) = delete;
    ~Task() { if (h) h.destroy(); }
    int run() { if (!h.done()) h.resume(); return h.promise().value; }
};

Task compute(int a, int b) {
    int x = co_await Awaitable{};   // suspends, then resumes with 42
    co_return a + b + x;
}

int main() {
    Task t = compute(1, 2);
    int r = t.run();
    std::printf("coroutine result=%d expected=45 -> %s\n", r, r == 45 ? "OK" : "FAIL");
    return r == 45 ? 0 : 1;
}
