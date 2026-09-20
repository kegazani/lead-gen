package prometheus

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

// apiResponse — конверт ответа HTTP API Prometheus.
type apiResponse struct {
	Status    string          `json:"status"`
	Data      json.RawMessage `json:"data"`
	Warnings  []string        `json:"warnings"`
	ErrorType string          `json:"errorType"`
	Error     string          `json:"error"`
}

// apiData — поле data конверта: вид результата и сам результат.
type apiData struct {
	ResultType string          `json:"resultType"`
	Result     json.RawMessage `json:"result"`
}

// samplePoint — точка в формате Prometheus: пара `[<время в секундах>, "<значение>"]`.
// Значение приходит строкой, чтобы выразить `NaN` и бесконечности.
type samplePoint struct {
	timestamp float64
	value     string
}

// UnmarshalJSON разбирает пару строго из двух элементов. Формат фиксирован
// протоколом: время — число (в т.ч. дробное), значение — строка.
func (p *samplePoint) UnmarshalJSON(data []byte) error {
	var raw []json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("точка не является массивом [время, значение]: %w", err)
	}
	if len(raw) != 2 {
		return fmt.Errorf("точка должна содержать ровно два элемента, получено %d", len(raw))
	}
	if err := json.Unmarshal(raw[0], &p.timestamp); err != nil {
		return fmt.Errorf("разбор времени точки: %w", err)
	}
	if err := json.Unmarshal(raw[1], &p.value); err != nil {
		return fmt.Errorf("разбор значения точки: %w", err)
	}
	return nil
}

// sample превращает точку в Sample. strconv.ParseFloat понимает формы `NaN`,
// `+Inf` и `-Inf`, которые Prometheus отдаёт строками.
func (p samplePoint) sample() (Sample, error) {
	v, err := strconv.ParseFloat(p.value, 64)
	if err != nil {
		return Sample{}, fmt.Errorf("значение %q не является числом: %w", p.value, err)
	}
	return Sample{Time: timeFromSeconds(p.timestamp), Value: v}, nil
}

// instantSeries — серия в результате vector: ровно одна точка.
type instantSeries struct {
	Metric map[string]string `json:"metric"`
	Value  samplePoint       `json:"value"`
}

// rangeSeries — серия в результате matrix: набор точек.
type rangeSeries struct {
	Metric map[string]string `json:"metric"`
	Values []samplePoint     `json:"values"`
}

// decodeData извлекает поле data успешного ответа.
func decodeData(env apiResponse) (apiData, error) {
	var data apiData
	if len(env.Data) == 0 {
		return data, fmt.Errorf("prometheus: ответ успешен, но поле data пусто")
	}
	if err := json.Unmarshal(env.Data, &data); err != nil {
		return data, fmt.Errorf("prometheus: разбор data: %w", err)
	}
	return data, nil
}

// parseVector разбирает result instant-запроса.
func parseVector(data apiData) (Vector, error) {
	var raw []instantSeries
	if err := json.Unmarshal(data.Result, &raw); err != nil {
		return nil, fmt.Errorf("разбор результата vector: %w", err)
	}
	out := make(Vector, 0, len(raw))
	for _, s := range raw {
		sample, err := s.Value.sample()
		if err != nil {
			return nil, fmt.Errorf("серия %s: %w", metricString(s.Metric), err)
		}
		out = append(out, Series{Metric: s.Metric, Samples: []Sample{sample}})
	}
	return out, nil
}

// parseMatrix разбирает result range-запроса.
func parseMatrix(data apiData) (Matrix, error) {
	var raw []rangeSeries
	if err := json.Unmarshal(data.Result, &raw); err != nil {
		return nil, fmt.Errorf("разбор результата matrix: %w", err)
	}
	out := make(Matrix, 0, len(raw))
	for _, s := range raw {
		samples := make([]Sample, 0, len(s.Values))
		for _, point := range s.Values {
			sample, err := point.sample()
			if err != nil {
				return nil, fmt.Errorf("серия %s: %w", metricString(s.Metric), err)
			}
			samples = append(samples, sample)
		}
		out = append(out, Series{Metric: s.Metric, Samples: samples})
	}
	return out, nil
}

// timeFromSeconds переводит секунды из ответа (возможно дробные) в time.Time.
// Разделение целой и дробной части сохраняет наносекундную точность без
// переполнения при больших значениях.
func timeFromSeconds(seconds float64) time.Time {
	if math.IsNaN(seconds) || math.IsInf(seconds, 0) {
		return time.Time{}
	}
	whole := math.Floor(seconds)
	nanos := int64(math.Round((seconds - whole) * float64(time.Second)))
	return time.Unix(int64(whole), nanos).UTC()
}

// formatTime печатает момент времени как число секунд (в т.ч. дробное).
func formatTime(t time.Time) string {
	sec := t.Unix()
	nsec := t.Nanosecond()
	if nsec == 0 {
		return strconv.FormatInt(sec, 10)
	}
	return strconv.FormatFloat(float64(sec)+float64(nsec)/float64(time.Second), 'f', -1, 64)
}

// formatStep печатает шаг в секундах с поддержкой дробных значений.
func formatStep(step time.Duration) string {
	return strconv.FormatFloat(step.Seconds(), 'f', -1, 64)
}

// metricString печатает метки серии в стабильном порядке — для сообщений об
// ошибках, чтобы одна и та же серия описывалась одинаково.
func metricString(m map[string]string) string {
	if len(m) == 0 {
		return "{}"
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	b.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(strconv.Quote(m[k]))
	}
	b.WriteByte('}')
	return b.String()
}
