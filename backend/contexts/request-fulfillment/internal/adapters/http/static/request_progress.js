// Обновление списка заявок: сервер остаётся владельцем статусов и подписей.
(function () {
  "use strict";
  var section = document.querySelector("[data-request-progress]");
  if (!section) return;

  var status = section.querySelector("[data-refresh-status]");
  var url = new URL(window.location.href);
  url.hash = "";
  url.searchParams.set("tenant_id", section.getAttribute("data-tenant-id") || "");

  async function refresh() {
    // Не прерываем навигацию с клавиатуры и не опрашиваем скрытую вкладку.
    if (!document.hidden && !section.querySelector("tbody").contains(document.activeElement)) {
      var controller = new AbortController();
      var timeout = setTimeout(function () { controller.abort(); }, 10000);
      try {
        var response = await fetch(url.toString(), {
          credentials: "same-origin", cache: "no-store", signal: controller.signal
        });
        if (!response.ok) throw new Error("refresh failed");
        var next = new DOMParser().parseFromString(await response.text(), "text/html");
        var nextBody = next.querySelector("[data-request-progress] tbody");
        if (!nextBody) throw new Error("request list missing");
        var body = section.querySelector("tbody");
        if (body.innerHTML !== nextBody.innerHTML) {
          body.replaceWith(nextBody);
          status.textContent = section.getAttribute("data-refresh-updated");
        } else {
          status.textContent = "";
        }
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
