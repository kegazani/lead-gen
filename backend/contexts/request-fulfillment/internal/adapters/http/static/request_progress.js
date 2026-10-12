// Обновление списка и карточки исполнения: сервер остаётся владельцем статусов и подписей.
(function () {
  "use strict";
  var section = document.querySelector("[data-request-progress]");
  if (!section) return;

  var status = section.querySelector("[data-refresh-status]");
  var selectors = section.getAttribute("data-request-progress") === "detail"
    ? ["[data-execution-summary]", "[data-execution-workflow]", "[data-execution-drafts]", "[data-execution-actions]"]
    : ["tbody"];
  var url = new URL(window.location.href);
  url.hash = "";
  url.searchParams.set("tenant_id", section.getAttribute("data-tenant-id") || "");

  async function refresh() {
    // Не прерываем навигацию с клавиатуры и не опрашиваем скрытую вкладку.
    if (!document.hidden && !selectors.some(function (selector) { return section.querySelector(selector).contains(document.activeElement); })) {
      var controller = new AbortController();
      var timeout = setTimeout(function () { controller.abort(); }, 10000);
      try {
        var response = await fetch(url.toString(), {
          credentials: "same-origin", cache: "no-store", signal: controller.signal
        });
        if (!response.ok) throw new Error("refresh failed");
        var next = new DOMParser().parseFromString(await response.text(), "text/html");
        var nextSection = next.querySelector("[data-request-progress]");
        if (!nextSection || nextSection.getAttribute("data-tenant-id") !== section.getAttribute("data-tenant-id")) {
          throw new Error("request context missing");
        }
        // Проверяем все блоки до подмены: ответ страницы входа или неполный
        // ответ не должны стереть последнее показанное состояние.
        var parts = selectors.map(function (selector) {
          var current = section.querySelector(selector);
          var replacement = nextSection.querySelector(selector);
          if (!current || !replacement) throw new Error("request fragment missing");
          return [current, replacement];
        });
        var updated = false;
        parts.forEach(function (part) {
          if (part[0].innerHTML !== part[1].innerHTML) {
            part[0].replaceWith(part[1]);
            updated = true;
          }
        });
        status.textContent = updated ? section.getAttribute("data-refresh-updated") : "";
      } catch (error) {
        // Ошибка обновления не стирает уже показанные заявки.
        status.textContent = section.getAttribute("data-refresh-error");
      } finally {
        clearTimeout(timeout);
      }
    }
    setTimeout(refresh, 5000);
  }
  setTimeout(refresh, 5000);
})();
