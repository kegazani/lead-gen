// C++20 <format> smoke test
#include <format>
#include <string>
#include <cstdio>

int main() {
    std::string s = std::format("hello {} {:04d} {:.2f}", "world", 7, 3.14159);
    std::puts(s.c_str());
    const std::string expect = "hello world 0007 3.14";
    bool ok = (s == expect);
    std::printf("format -> %s\n", ok ? "OK" : "FAIL");
    return ok ? 0 : 1;
}
