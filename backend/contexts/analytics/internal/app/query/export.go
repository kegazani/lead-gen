package query

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"time"
)

// Типы ответа выгрузки отчёта. Значения совпадают с тем, что отдаёт синхронная
// выгрузка, поэтому длительная операция и прямой экспорт не расходятся.
const (
	// CSVContentType — тип ответа CSV с BOM.
	CSVContentType = "text/csv; charset=utf-8"
	// XLSXContentType — тип ответа книги XLSX.
	XLSXContentType = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
)

// RenderCSV собирает тело выгрузки в CSV. Файл начинается с BOM, чтобы Excel
// читал русские заголовки как UTF-8. Срезы ниже порога минимальной группы в файл
// не попадают: их решает ExportRows.
func RenderCSV(view ReportView) ([]byte, error) {
	var buffer bytes.Buffer
	buffer.WriteString("\ufeff")
	writer := csv.NewWriter(&buffer)
	for _, row := range ExportRows(view) {
		if err := writer.Write(row); err != nil {
			return nil, fmt.Errorf("query: сборка CSV: %w", err)
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return nil, fmt.Errorf("query: сборка CSV: %w", err)
	}
	return buffer.Bytes(), nil
}

// RenderXLSX собирает тело выгрузки в книгу XLSX. Набор данных тот же, что у CSV,
// и так же не раскрывает малую группу.
func RenderXLSX(view ReportView) ([]byte, error) {
	var buffer bytes.Buffer
	if err := WriteXLSX(&buffer, "Отчёт", ExportRows(view)); err != nil {
		return nil, fmt.Errorf("query: сборка XLSX: %w", err)
	}
	return buffer.Bytes(), nil
}

// ExportFilename собирает имя файла выгрузки с датой момента сборки.
func ExportFilename(extension string, at time.Time) string {
	return "report-" + at.UTC().Format("20060102") + "." + extension
}
