package query

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// WriteXLSX собирает книгу XLSX (Office Open XML) из таблицы строк одного листа.
// Генератор намеренно минимален и опирается только на стандартную библиотеку:
// книга содержит один лист с ячейками inlineStr/число, чего достаточно для
// выгрузки отчёта, и открывается в Excel и совместимых таблицах.
//
// Сборка файла живёт в прикладном слое, а не в HTTP-адаптере: тот же артефакт
// собирает и длительная операция экспорта, а прикладной слой не вправе зависеть
// от адаптера.
func WriteXLSX(w io.Writer, sheet string, rows [][]string) error {
	if strings.TrimSpace(sheet) == "" {
		sheet = "Отчёт"
	}
	archive := zip.NewWriter(w)

	parts := []struct {
		name string
		body string
	}{
		{"[Content_Types].xml", contentTypesXML},
		{"_rels/.rels", rootRelsXML},
		{"xl/workbook.xml", workbookXML(sheet)},
		{"xl/_rels/workbook.xml.rels", workbookRelsXML},
		{"xl/styles.xml", stylesXML},
		{"xl/worksheets/sheet1.xml", sheetXML(rows)},
	}
	for _, part := range parts {
		file, err := archive.Create(part.name)
		if err != nil {
			_ = archive.Close()
			return fmt.Errorf("xlsx: создание части %s: %w", part.name, err)
		}
		if _, err := io.WriteString(file, part.body); err != nil {
			_ = archive.Close()
			return fmt.Errorf("xlsx: запись части %s: %w", part.name, err)
		}
	}
	if err := archive.Close(); err != nil {
		return fmt.Errorf("xlsx: закрытие книги: %w", err)
	}
	return nil
}

// sheetXML собирает тело листа: строки и ячейки. Пустые строки таблицы
// пропускаются, чтобы разрывы разделов не создавали пустых строк.
func sheetXML(rows [][]string) string {
	var builder strings.Builder
	builder.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	builder.WriteString(`<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>`)
	rowNumber := 0
	for _, row := range rows {
		if len(row) == 0 {
			continue
		}
		rowNumber++
		builder.WriteString(`<row r="` + strconv.Itoa(rowNumber) + `">`)
		for column, value := range row {
			builder.WriteString(cellXML(column, rowNumber, value))
		}
		builder.WriteString(`</row>`)
	}
	builder.WriteString(`</sheetData></worksheet>`)
	return builder.String()
}

// cellXML собирает ячейку: число как числовое значение, строку — как inlineStr.
func cellXML(column, row int, value string) string {
	if value == "" {
		return ""
	}
	ref := columnRef(column) + strconv.Itoa(row)
	if _, err := strconv.ParseFloat(value, 64); err == nil {
		return `<c r="` + ref + `"><v>` + value + `</v></c>`
	}
	return `<c r="` + ref + `" t="inlineStr"><is><t xml:space="preserve">` + escapeXML(value) + `</t></is></c>`
}

// columnRef переводит индекс колонки (0-based) в буквенную ссылку A, B, …, AA.
func columnRef(index int) string {
	name := ""
	for index >= 0 {
		name = string(rune('A'+index%26)) + name
		index = index/26 - 1
	}
	return name
}

// escapeXML экранирует значение для включения в XML-тело.
func escapeXML(value string) string {
	var buffer bytes.Buffer
	if err := xml.EscapeText(&buffer, []byte(value)); err != nil {
		// xml.EscapeText не падает на корректном буфере; на всякий случай отдаём
		// исходное значение, а не пустое.
		return value
	}
	return buffer.String()
}

func workbookXML(sheet string) string {
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" ` +
		`xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">` +
		`<sheets><sheet name="` + escapeXML(sheet) + `" sheetId="1" r:id="rId1"/></sheets></workbook>`
}

const contentTypesXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
	`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
	`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>` +
	`<Default Extension="xml" ContentType="application/xml"/>` +
	`<Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>` +
	`<Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>` +
	`<Override PartName="/xl/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.styles+xml"/>` +
	`</Types>`

const rootRelsXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
	`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
	`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/>` +
	`</Relationships>`

const workbookRelsXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
	`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
	`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/>` +
	`<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/>` +
	`</Relationships>`

const stylesXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
	`<styleSheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">` +
	`<fonts count="1"><font><sz val="11"/><name val="Calibri"/></font></fonts>` +
	`<fills count="2"><fill><patternFill patternType="none"/></fill><fill><patternFill patternType="gray125"/></fill></fills>` +
	`<borders count="1"><border/></borders>` +
	`<cellStyleXfs count="1"><xf/></cellStyleXfs>` +
	`<cellXfs count="1"><xf/></cellXfs>` +
	`</styleSheet>`
