// Снимок заменяется целиком: счётчики и список принадлежат одной версии миссии.
(function () {
  "use strict";
  var page = document.querySelector("[data-research-page]");
  if (!page) return;
  var status = page.querySelector("[data-research-refresh-status]");
  var endpoint = new URL(window.location.href);
  endpoint.pathname = endpoint.pathname.replace(/\/$/, "") + "/progress";
  endpoint.hash = "";
  var etag = "";
  var stopped = false;

  async function refresh() {
    if (stopped) return;
    var current = page.querySelector("[data-research-snapshot]");
    if (!document.hidden && current && !current.contains(document.activeElement)) {
      var controller = new AbortController();
      var timeout = setTimeout(function () { controller.abort(); }, 10000);
      try {
        var response = await fetch(endpoint.toString(), {
          credentials: "same-origin", cache: "no-store", redirect: "error", signal: controller.signal,
          headers: etag ? { "If-None-Match": etag } : {}
        });
        if (response.status === 401) {
          stopped = true;
          status.textContent = page.getAttribute("data-session-expired");
          page.querySelector("[data-research-login]").hidden = false;
          return;
        }
        if (response.status === 304) { status.textContent = ""; return; }
        if (!response.ok) throw new Error("refresh unavailable");
        var documentNext = new DOMParser().parseFromString(await response.text(), "text/html");
        var replacement = documentNext.querySelector("[data-research-snapshot]");
        if (!replacement || replacement.getAttribute("data-tenant-id") !== page.getAttribute("data-tenant-id") || replacement.getAttribute("data-request-id") !== page.getAttribute("data-request-id")) throw new Error("wrong request scope");
        var revision = Number(replacement.getAttribute("data-revision"));
        var priorRevision = Number(current.getAttribute("data-revision"));
        var priorMission = current.getAttribute("data-mission-id");
        var nextMission = replacement.getAttribute("data-mission-id");
        if (!Number.isSafeInteger(revision) || revision < 0) throw new Error("invalid revision");
        // Новый запуск требует полной навигации: запоздалый ответ другой миссии
        // не может подменить открытый снимок. Начальный пустой экран исключён.
        if (priorMission && nextMission !== priorMission) throw new Error("mission changed");
        if (revision > priorRevision) {
          current.replaceWith(replacement);
          etag = response.headers.get("ETag") || "";
          status.textContent = page.getAttribute("data-refresh-updated");
        }
      } catch (error) {
        status.textContent = page.getAttribute("data-refresh-error");
      } finally {
        clearTimeout(timeout);
      }
    }
  }
  async function poll() {
    await refresh();
    if (!stopped) setTimeout(poll, 5000);
  }
  setTimeout(poll, 5000);
})();
