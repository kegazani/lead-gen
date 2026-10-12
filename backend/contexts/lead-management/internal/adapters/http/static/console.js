// Консоль оператора: три независимых улучшения.
//
// 1. Сохранение выбора заданий между авто-обновлениями журнала. Журнал
//    обновляется HTMX и заменяется целиком, поэтому отметки чекбоксов
//    восстанавливаются после каждой подстановки.
// 2. Перенос лида между этапами воронки перетаскиванием. Это прогрессивное
//    улучшение: без JavaScript перенос остаётся доступен формой в каждой
//    колонке (доступный с клавиатуры fallback).
// 3. Сохранение раскрытых строк журнала между теми же авто-обновлениями: тот же
//    приём, что и для чекбоксов, — состояние раскрытия живёт в памяти страницы и
//    восстанавливается после подстановки фрагмента.
//
// CSP консоли разрешает скрипты только с того же источника и запрещает
// inline-скрипты и inline-обработчики, поэтому здесь только addEventListener.
// Доменные правила здесь не дублируются: остров лишь отправляет выбор на
// существующий эндпоинт, а решение принимает серверный сценарий.
(function () {
  'use strict';

  // adminBasePath возвращает внешний префикс пути консоли под единым origin
  // (admin-proxy). Префикс вставляет сервер в <meta name="admin-base-path">,
  // чтобы адрес переноса лида не дублировался в скрипте.
  function adminBasePath() {
    var meta = document.querySelector('meta[name="admin-base-path"]');
    return meta && meta.content ? meta.content : '';
  }

  // ==========================================================
  // 1. Сохранение выбора заданий между обновлениями журнала
  // ==========================================================

  // Отмеченные идентификаторы заданий. Живут до перезагрузки страницы.
  var selected = new Set();

  // jobBoxes возвращает чекбоксы заданий текущего журнала.
  function jobBoxes() {
    return Array.prototype.slice.call(document.querySelectorAll('input[data-select-job]'));
  }

  // selectAllBox возвращает чекбокс «выбрать все» в шапке таблицы.
  function selectAllBox() {
    return document.querySelector('input[data-select-all]');
  }

  // sync приводит разметку журнала в соответствие с выбранными идентификаторами:
  // восстанавливает отметки после подстановки фрагмента, снимает выбор с заданий,
  // ушедших из журнала или ставших терминальными, обновляет счётчик, состояние
  // «выбрать все» и доступность кнопки.
  function sync() {
    var boxes = jobBoxes();
    var present = new Set();

    boxes.forEach(function (box) {
      if (box.disabled) {
        // Терминальные задания отметке не подлежат: снимаем и не отправляем.
        box.checked = false;
        selected.delete(box.value);
        return;
      }
      present.add(box.value);
      box.checked = selected.has(box.value);
    });

    // Задания, исчезнувшие из журнала, больше не участвуют в выборе.
    Array.prototype.slice.call(Array.from(selected)).forEach(function (id) {
      if (!present.has(id)) {
        selected.delete(id);
      }
    });

    var checked = boxes.filter(function (box) {
      return box.checked;
    }).length;

    var all = selectAllBox();
    if (all) {
      all.checked = boxes.length > 0 && checked === boxes.length;
      all.indeterminate = checked > 0 && checked < boxes.length;
      all.disabled = boxes.length === 0;
    }

    var counter = document.querySelector('[data-selected-count]');
    if (counter) {
      counter.textContent = 'выбрано: ' + checked;
    }

    var button = document.querySelector('[data-cancel-selected]');
    if (button) {
      button.disabled = checked === 0;
    }
  }

  // change ловит отметки строк и «выбрать все». Слушатель на документе переживает
  // замену фрагмента журнала, поэтому перепривязки не требует.
  document.addEventListener('change', function (event) {
    var target = event.target;
    if (!(target instanceof HTMLInputElement) || target.type !== 'checkbox') {
      return;
    }

    if (target.hasAttribute('data-select-job')) {
      if (target.checked) {
        selected.add(target.value);
      } else {
        selected.delete(target.value);
      }
      sync();
      return;
    }

    if (target.hasAttribute('data-select-all')) {
      var mark = target.checked;
      jobBoxes().forEach(function (box) {
        if (box.disabled) {
          return;
        }
        if (mark) {
          selected.add(box.value);
        } else {
          selected.delete(box.value);
        }
      });
      sync();
    }
  });

  // ==========================================================
  // 4. Сохранение раскрытых строк журнала между обновлениями
  // ==========================================================

  // expanded — идентификаторы раскрытых заданий. Живут до перезагрузки страницы.
  var expanded = new Set();

  // journalItems возвращает раскрывающиеся строки текущего журнала.
  function journalItems() {
    return Array.prototype.slice.call(document.querySelectorAll('details[data-job-detail]'));
  }

  // syncExpanded приводит разметку журнала в соответствие с раскрытыми
  // идентификаторами: восстанавливает раскрытие после подстановки фрагмента,
  // помечает раскрытые строки атрибутом data-expanded и забывает задания,
  // ушедшие из журнала.
  function syncExpanded() {
    var items = journalItems();
    var present = new Set();

    items.forEach(function (item) {
      var id = item.getAttribute('data-job-id');
      if (!id) {
        return;
      }
      present.add(id);
      var open = expanded.has(id);
      item.open = open;
      if (open) {
        item.setAttribute('data-expanded', '');
      } else {
        item.removeAttribute('data-expanded');
      }
    });

    Array.prototype.slice.call(Array.from(expanded)).forEach(function (id) {
      if (!present.has(id)) {
        expanded.delete(id);
      }
    });
  }

  // toggle ловит ручное раскрытие и сворачивание строк. Событие toggle не
  // всплывает, поэтому слушатель взят на фазе перехвата; на документе он переживает
  // замену фрагмента журнала и перепривязки не требует.
  document.addEventListener('toggle', function (event) {
    var item = event.target;
    if (!(item instanceof HTMLDetailsElement) || !item.hasAttribute('data-job-detail')) {
      return;
    }
    var id = item.getAttribute('data-job-id');
    if (!id) {
      return;
    }
    if (item.open) {
      expanded.add(id);
    } else {
      expanded.delete(id);
    }
    syncExpanded();
  }, true);

  // HTMX заменяет фрагмент журнала целиком: после каждой подстановки и
  // стабилизации возвращаем и отметки, и раскрытие строк.
  document.addEventListener('htmx:afterSwap', function () {
    sync();
    syncExpanded();
  });
  document.addEventListener('htmx:afterSettle', function () {
    sync();
    syncExpanded();
  });

  // ==========================================================
  // 2. Перенос лида между этапами перетаскиванием
  // ==========================================================

  // dragging — перетаскиваемый лид текущего жеста.
  var dragging = null;

  // leadFrom возвращает элемент переносимого лида для цели события.
  function leadFrom(target) {
    return target instanceof Element ? target.closest('.lead') : null;
  }

  // stageColumnFrom возвращает колонку этапа для цели события.
  function stageColumnFrom(target) {
    return target instanceof Element ? target.closest('.stage-col[data-stage]') : null;
  }

  // moveLead отправляет перенос на существующий эндпоинт формы: тот же маршрут,
  // то же поле stage и тот же CSRF-токен. Стадия берётся из колонки-приёмника и
  // здесь не проверяется — правило остаётся на сервере.
  function moveLead(lead, column) {
    var leadId = lead.getAttribute('data-lead');
    var stage = column.getAttribute('data-stage');
    var tokenField = lead.querySelector('input[name="csrf"]');
    if (!leadId || !stage || !tokenField) {
      return;
    }

    var body = new URLSearchParams();
    body.set('stage', stage);
    body.set('csrf', tokenField.value);

    column.classList.add('is-heating');
    window.setTimeout(function () {
      column.classList.remove('is-heating');
    }, 650);

    fetch(adminBasePath() + '/funnel/' + encodeURIComponent(leadId) + '/stage', {
      method: 'POST',
      credentials: 'same-origin',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body: body.toString(),
    })
      .then(function () {
        // Сервер выполняет перенос и отвечает redirect (POST/redirect/GET);
        // клиент только перечитывает состояние.
        window.location.assign(adminBasePath() + '/funnel');
      })
      .catch(function () {
        // Сеть откажет — перенос остаётся доступен формой в колонке.
      });
  }

  // dragstart запоминает лид и включает визуальное состояние перетаскивания.
  document.addEventListener('dragstart', function (event) {
    var lead = leadFrom(event.target);
    if (!lead) {
      return;
    }
    dragging = lead;
    lead.classList.add('is-dragging');
    if (event.dataTransfer) {
      event.dataTransfer.effectAllowed = 'move';
      event.dataTransfer.setData('text/plain', lead.getAttribute('data-lead') || '');
    }
  });

  // dragend снимает визуальное состояние перетаскивания.
  document.addEventListener('dragend', function () {
    if (dragging) {
      dragging.classList.remove('is-dragging');
      dragging = null;
    }
    Array.prototype.slice.call(document.querySelectorAll('.stage-col.is-over')).forEach(function (col) {
      col.classList.remove('is-over');
    });
  });

  // dragover разрешает сброс и подсвечивает колонку-приёмник.
  document.addEventListener('dragover', function (event) {
    var column = stageColumnFrom(event.target);
    if (!column || !dragging) {
      return;
    }
    event.preventDefault();
    if (event.dataTransfer) {
      event.dataTransfer.dropEffect = 'move';
    }
    if (!column.classList.contains('is-over')) {
      column.classList.add('is-over');
    }
  });

  // dragleave снимает подсветку колонки, когда указатель её покидает.
  document.addEventListener('dragleave', function (event) {
    var column = stageColumnFrom(event.target);
    if (!column) {
      return;
    }
    if (!column.contains(event.relatedTarget)) {
      column.classList.remove('is-over');
    }
  });

  // drop выполняет перенос на колонку-приёмник.
  document.addEventListener('drop', function (event) {
    var column = stageColumnFrom(event.target);
    if (!column || !dragging) {
      return;
    }
    event.preventDefault();
    column.classList.remove('is-over');
    moveLead(dragging, column);
  });

  	// ==========================================================
  	// 3. Вставка переменной шаблона в тему или тело письма
  	// ==========================================================

  	// lastField — последнее поле темы/тела, получившее фокус. Вставка идёт в него,
  	// а если фокус не задан — в поле по умолчанию (тело письма).
  	var lastField = null;

  	document.addEventListener('focusin', function (event) {
  		var target = event.target;
  		if (!(target instanceof HTMLInputElement) && !(target instanceof HTMLTextAreaElement)) {
  			return;
  		}
  		if (target.hasAttribute('data-placeholder-field')) {
  			lastField = target;
  		}
  	});

  	// insertPlaceholder дописывает плейсхолдер переменной в поле шаблона. Правила
  	// подстановки и проверка объявленных переменных остаются на сервере: здесь
  	// только текстовая вставка, поэтому доменные правила не дублируются.
  	document.addEventListener('click', function (event) {
  		var button = event.target instanceof Element ? event.target.closest('[data-insert-placeholder]') : null;
  		if (!button) {
  			return;
  		}
  		var key = button.getAttribute('data-insert-placeholder');
  		if (!key) {
  			return;
  		}
  		var field = lastField || document.querySelector('[data-placeholder-field][data-placeholder-default]');
  		if (!field) {
  			return;
  		}
		var token = '{{' + key + '}}';
		var start = field.selectionStart === null ? field.value.length : field.selectionStart;
		var end = field.selectionEnd === null ? field.value.length : field.selectionEnd;
		field.value = field.value.slice(0, start) + token + field.value.slice(end);
		field.focus();
		var caret = start + token.length;
		if (field.setSelectionRange) {
			field.setSelectionRange(caret, caret);
		}
		// Вставка переменной объявляет её у шаблона: отметка формы добавляет ключ в
		// объявленный набор, но саму проверку выполняет сервер.
		var declaration = document.querySelector('input[name="var_' + key + '"]');
		if (declaration) {
			declaration.checked = true;
		}
	});

	  // ==========================================================
	  // 5. Переключатель представления списка карточек
	  // ==========================================================

	  // cardsViewKey — ключ соглашения острова: хранит только выбор вида, без
	  // персональных данных. Вид по умолчанию (плитки) отрисован сервером, поэтому
	  // без JavaScript список остаётся читаемым.
	  var cardsViewKey = 'leadgen.cardsView';

	  // cardsContainer возвращает контейнер списка карточек.
	  function cardsContainer() {
	    return document.querySelector('[data-cards-view]');
	  }

	  // cardsViewButtons возвращает кнопки переключателя вида.
	  function cardsViewButtons() {
	    return Array.prototype.slice.call(document.querySelectorAll('[data-view-set]'));
	  }

	  // applyCardsView приводит список к выбранному виду: класс на контейнере и
	  // состояние кнопок. Неизвестное значение сводится к виду по умолчанию.
	  function applyCardsView(view) {
	    if (view !== 'rows' && view !== 'tiles') {
	      view = 'tiles';
	    }
	    var container = cardsContainer();
	    if (container) {
	      container.setAttribute('data-cards-view', view);
	    }
	    cardsViewButtons().forEach(function (button) {
	      var active = button.getAttribute('data-view-set') === view;
	      button.setAttribute('aria-pressed', active ? 'true' : 'false');
	      button.classList.toggle('is-active', active);
	    });
	  }

	  // storedCardsView читает запомненный вид. Доступ к localStorage может быть
	  // запрещён (приватный режим), тогда вид остаётся по умолчанию.
	  function storedCardsView() {
	    try {
	      return window.localStorage.getItem(cardsViewKey);
	    } catch (error) {
	      return null;
	    }
	  }

	  // Состав результатов — вторая, независимая от вида ось. Она хранится под своим
	  // ключом: переключение состава не меняет вид «плитки/строки» и наоборот.
	  var cardsCompositionKey = 'leadgen.cardsComposition';

	  // cardsCompositionValue сводит неизвестный состав к значению по умолчанию
	  // («Подходящие»). Состав — четыре группы; неизвестное или устаревшее значение
	  // (в частности прежнее «other») сводится к группе по умолчанию.
	  function cardsCompositionValue(raw) {
	    switch (raw) {
	      case 'hypothesis':
	      case 'unsuitable':
	      case 'unassessed':
	        return raw;
	      default:
	        return 'fits';
	    }
	  }

	  // cardsCompositionButtons возвращает кнопки переключателя состава.
	  function cardsCompositionButtons() {
	    return Array.prototype.slice.call(document.querySelectorAll('[data-composition-set]'));
	  }

	  // applyCardsComposition приводит список к выбранному составу: атрибут на
	  // контейнере и состояние кнопок. На вид (data-cards-view) не влияет.
	  function applyCardsComposition(composition) {
	    composition = cardsCompositionValue(composition);
	    var container = cardsContainer();
	    if (container) {
	      container.setAttribute('data-cards-composition', composition);
	    }
	    cardsCompositionButtons().forEach(function (button) {
	      var active = button.getAttribute('data-composition-set') === composition;
	      button.setAttribute('aria-pressed', active ? 'true' : 'false');
	      button.classList.toggle('is-active', active);
	    });
	  }

	  // storedCardsComposition читает запомненный состав (при недоступном localStorage
	  // остаётся значение по умолчанию).
	  function storedCardsComposition() {
	    try {
	      return window.localStorage.getItem(cardsCompositionKey);
	    } catch (error) {
	      return null;
	    }
	  }

	  // cardsFilterActive сообщает, активен ли серверный фильтр по причине отсева.
	  // При активном фильтре запомненный состав не применяется при загрузке:
	  // показывается вся отфильтрованная выборка, а не одна группа.
	  function cardsFilterActive() {
	    var container = cardsContainer();
	    return !!container && container.getAttribute('data-cards-filter') === 'true';
	  }

	  // click ловит переключение состава: выбор применяется без запроса к серверу и
	  // не трогает выбранный вид.
	  document.addEventListener('click', function (event) {
	    var button = event.target instanceof Element ? event.target.closest('[data-composition-set]') : null;
	    if (!button) {
	      return;
	    }
	    var composition = cardsCompositionValue(button.getAttribute('data-composition-set'));
	    applyCardsComposition(composition);
	    try {
	      window.localStorage.setItem(cardsCompositionKey, composition);
	    } catch (error) {
	      // Запоминание недоступно — состав всё равно применён до перезагрузки.
	    }
	  });

	  // click ловит переключение вида: выбор применяется без запроса к серверу.
	  document.addEventListener('click', function (event) {
	    var button = event.target instanceof Element ? event.target.closest('[data-view-set]') : null;
	    if (!button) {
	      return;
	    }
	    var view = button.getAttribute('data-view-set');
	    applyCardsView(view);
	    try {
	      window.localStorage.setItem(cardsViewKey, view === 'rows' ? 'rows' : 'tiles');
	    } catch (error) {
	      // Запоминание недоступно — вид всё равно применён до перезагрузки.
	    }
	  });

	  // Выбор вида и состава применяется при загрузке независимо: каждый
	  // переживает перезагрузку и не сбрасывает другой. При активном фильтре по
	  // причине отсева запомненный состав не применяется: показывается вся
	  // отфильтрованная выборка.
	  applyCardsView(storedCardsView());
	  if (!cardsFilterActive()) {
	    applyCardsComposition(storedCardsComposition());
	  }

	  // Скрипт подключён с defer: разметка уже разобрана, начальная синхронизация
	  // сразу отключает кнопку при пустом выборе и восстанавливает раскрытие строк.
	  sync();
	  syncExpanded();
})();
