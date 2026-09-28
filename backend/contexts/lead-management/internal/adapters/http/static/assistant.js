// Ассистент: пошаговый вывод ответа агента через поток серверных событий.
//
// Это прогрессивное улучшение, а не обязательный путь. Без JavaScript форма
// [data-assistant-form] отправляется обычным POST /assistant и экран работает как
// прежде. Остров перехватывает только ход диалога («Отправить»), открывает
// POST /assistant/stream, читает кадры SSE из тела ответа и дописывает текст по
// мере генерации. Подтверждение запуска («Запустить краулинг») остров не
// перехватывает — оно уходит обычной формой.
//
// Доменных правил здесь нет: остров показывает значения, которые прислал сервер
// (готовые чипы параметров, цели плана, признак готовности), и не решает сам ни
// лимитов, ни готовности плана.
(function () {
  'use strict';

  var form = document.querySelector('form[data-assistant-form]');
  if (!form || typeof window.fetch !== 'function' || typeof window.ReadableStream !== 'function') {
    return;
  }

  var dialogue = document.querySelector('[data-dialogue]');
  var status = document.querySelector('[data-assistant-status]');
  var planSection = document.querySelector('[data-plan-section]');
  var messageField = form.querySelector('#message');
  var historyField = form.querySelector('input[name="history"]');
  var planField = form.querySelector('input[name="plan"]');
  var csrfField = form.querySelector('input[name="csrf"]');

  if (!dialogue || !messageField || !historyField || !planField || !csrfField) {
    return;
  }

  // setStatus объявляет состояние хода в aria-live области.
  function setStatus(text) {
    if (status) {
      status.textContent = text || '';
    }
  }

  // setBusy блокирует повторную отправку на время стрима: повтор хода снова
  // обратился бы к модели и удвоил расход токенов.
  function setBusy(busy) {
    var buttons = form.querySelectorAll('button[type="submit"]');
    Array.prototype.slice.call(buttons).forEach(function (button) {
      button.disabled = busy;
    });
  }

  // appendBubble добавляет пузырь ленты и возвращает его элемент.
  function appendBubble(role, text) {
    var empty = dialogue.querySelector('.empty');
    if (empty) {
      empty.remove();
    }
    var li = document.createElement('li');
    li.className = 'dialogue-' + role;
    li.textContent = text || '';
    dialogue.appendChild(li);
    return li;
  }

  // interpretationText добавляет в dl пару «подпись — значение» обычным текстом
  // (без вставки пользовательских строк как разметки).
  function interpretationText(dl, label, value) {
    var dt = document.createElement('dt');
    dt.textContent = label;
    var dd = document.createElement('dd');
    dd.textContent = value || '';
    dl.appendChild(dt);
    dl.appendChild(dd);
  }

  // interpretationList добавляет в dl пару «подпись — список». Пустой список
  // показывается прочерком: значения не выдумываются.
  function interpretationList(dl, label, items) {
    var dt = document.createElement('dt');
    dt.textContent = label;
    var dd = document.createElement('dd');
    var ul = document.createElement('ul');
    ul.className = 'interpretation-list';
    var list = items || [];
    if (list.length === 0) {
      var empty = document.createElement('li');
      empty.className = 'empty';
      empty.textContent = '—';
      ul.appendChild(empty);
    } else {
      list.forEach(function (item) {
        var li = document.createElement('li');
        li.textContent = item;
        ul.appendChild(li);
      });
    }
    dd.appendChild(ul);
    dl.appendChild(dt);
    dl.appendChild(dd);
  }

  // renderInterpretation перестраивает блок «Как система поняла запрос» отдельно
  // от плана обхода: критерии релевантности и технический план — разные сущности.
  function renderInterpretation(interpretation) {
    var body = document.querySelector('[data-interpretation-body]');
    if (!body) {
      return;
    }
    body.innerHTML = '';
    if (!interpretation || !interpretation.Defined) {
      var none = document.createElement('p');
      none.className = 'hint';
      none.textContent = 'Интерпретация не определена.';
      body.appendChild(none);
      return;
    }
    if (interpretation.Uncertainty) {
      var uncertain = document.createElement('p');
      uncertain.className = 'hint';
      uncertain.textContent = 'Недостаточно данных, чтобы определить признаки потребности. Уточните запрос — критерии не выдумываются.';
      body.appendChild(uncertain);
      return;
    }
    var dl = document.createElement('dl');
    dl.className = 'interpretation';
    interpretationText(dl, 'Целевой лид', interpretation.TargetLead);
    interpretationList(dl, 'Признаки потребности', interpretation.NeedSignals);
    interpretationList(dl, 'Исключения', interpretation.Exclusions);
    interpretationList(dl, 'Признаки гипотезы', interpretation.HypothesisSignals);
    body.appendChild(dl);
  }

  // renderPlan обновляет панель плана и блок интерпретации: цели и чипы параметров
  // приходят готовыми строками от сервера. Интерпретация — отдельный блок,
  // независимый от целей и лимитов обхода.
  function renderPlan(frame) {
    if (frame && frame.interpretation !== undefined) {
      renderInterpretation(frame.interpretation);
    }
    if (!planSection) {
      return;
    }
    var targets = planSection.querySelector('[data-plan-targets]');
    if (targets) {
      targets.innerHTML = '';
      (frame.targets || []).forEach(function (t) {
        var li = document.createElement('li');
        li.textContent = t.Kind + ': ' + t.Value;
        targets.appendChild(li);
      });
    }
    var chips = planSection.querySelector('[data-plan-limits]');
    if (chips) {
      chips.innerHTML = '';
      (frame.limits || []).forEach(function (c) {
        var li = document.createElement('li');
        li.className = 'chip';
        var label = document.createElement('span');
        label.className = 'chip-label';
        label.textContent = c.Label;
        var value = document.createElement('span');
        value.className = 'chip-value';
        value.textContent = c.Value;
        li.appendChild(label);
        li.appendChild(document.createTextNode(' '));
        li.appendChild(value);
        chips.appendChild(li);
      });
    }
    var ready = planSection.querySelector('[data-plan-ready]');
    if (ready) {
      ready.hidden = !frame.ready;
    }
  }

  // dispatch разбирает один кадр SSE и вызывает обработчик события.
  function dispatch(rawFrame, handlers) {
    var event = 'message';
    var data = '';
    rawFrame.split('\n').forEach(function (line) {
      if (line.indexOf('event:') === 0) {
        event = line.slice(6).trim();
      } else if (line.indexOf('data:') === 0) {
        data += line.slice(5).trim();
      }
    });
    var handler = handlers[event];
    if (!handler) {
      return;
    }
    var payload = {};
    if (data) {
      try {
        payload = JSON.parse(data);
      } catch (err) {
        return;
      }
    }
    handler(payload);
  }

  // readStream читает тело ответа и разбирает кадры, разделённые пустой строкой.
  function readStream(stream, handlers) {
    var reader = stream.getReader();
    var decoder = new TextDecoder();
    var buffer = '';

    function pump() {
      return reader.read().then(function (result) {
        if (result.done) {
          return;
        }
        buffer += decoder.decode(result.value, { stream: true });
        var idx;
        while ((idx = buffer.indexOf('\n\n')) >= 0) {
          var frame = buffer.slice(0, idx);
          buffer = buffer.slice(idx + 2);
          dispatch(frame, handlers);
        }
        return pump();
      });
    }

    return pump();
  }

  // streamTurn выполняет ход диалога потоком: дописывает реплику агента по мере
  // генерации, обновляет панель плана по завершающим кадрам.
  function streamTurn(text) {
    appendBubble('operator', text);
    var agent = appendBubble('agent', '');
    setStatus('Ассистент печатает…');
    setBusy(true);

    var body = new URLSearchParams();
    body.set('message', text);
    body.set('history', historyField.value);
    body.set('plan', planField.value);
    body.set('csrf', csrfField.value);

    fetch('/assistant/stream', {
      method: 'POST',
      credentials: 'same-origin',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body: body.toString(),
    })
      .then(function (response) {
        if (!response.ok || !response.body) {
          throw new Error('HTTP ' + response.status);
        }
        return readStream(response.body, {
          delta: function (frame) {
            agent.textContent += frame.text || '';
          },
          plan: renderPlan,
          done: function (frame) {
            // Непотоковый ответ (например, сообщение о пределе ходов) приходит
            // только завершающим кадром — тогда текст берём из него.
            if (!agent.textContent.trim() && frame.text) {
              agent.textContent = frame.text;
            }
            historyField.value = frame.history || '';
            planField.value = frame.plan || '';
          },
          error: function (frame) {
            setStatus(frame.message || 'Не удалось получить ответ ассистента.');
            // Сервер прикладывает историю к кадру ошибки: ход оператора должен
            // сохраниться, иначе подтверждение запуска не найдёт задачу.
            historyField.value = frame.history || '';
          },
        });
      })
      .then(function () {
        messageField.value = '';
        setStatus('');
      })
      .catch(function () {
        setStatus('Обрыв связи. Ход можно повторить, не начиная диалог заново.');
      })
      .then(function () {
        setBusy(false);
      });
  }

  form.addEventListener('submit', function (event) {
    // Перехватываем только ход диалога; запуск уходит обычной формой.
    var action = event.submitter ? event.submitter.value : 'turn';
    if (action !== 'turn') {
      return;
    }
    var text = messageField.value.trim();
    if (text === '') {
      // Пустой ответ пусть проверяет базовая форма/сервер: прогрессивное
      // улучшение не должно менять поведение без JavaScript.
      return;
    }
    event.preventDefault();
    streamTurn(text);
  });
})();
