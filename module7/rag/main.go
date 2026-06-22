// Package main สาธิต RAG (Retrieval-Augmented Generation) System
// รันด้วย: go run main.go
//
// RAG คืออะไร?
//
//	แทนที่จะให้ LLM ตอบจาก training data อย่างเดียว
//	เรา "retrieve" ข้อมูลที่เกี่ยวข้องก่อน แล้วส่งให้ LLM ใช้ตอบ
//	ข้อดี: ข้อมูลเป็นปัจจุบัน, อ้างอิงแหล่งได้, ลด hallucination
//
// Flow:
//
//	Ingest: documents → chunk → embed → store ใน vector store
//	Query:  question → embed → similarity search → retrieve top-K → LLM generate
package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log"
	"math"
	"sort"
	"strings"
	"sync"
	"time"
)

// ============================================================
// SECTION 1: Document และ Chunk
// ============================================================

// RawDocument — เอกสารต้นฉบับก่อน chunking
type RawDocument struct {
	ID       string
	Title    string
	Source   string // เช่น "company-policy-hr.pdf"
	Content  string
	Metadata map[string]string
}

// Chunk — ส่วนย่อยของเอกสารที่ผ่าน chunking แล้ว
// ทำไมต้อง chunk?
//   - LLM มี context window จำกัด (ใส่ทั้งเอกสารไม่ได้)
//   - Embedding ของข้อความยาวๆ จะ "diluted" (คุณภาพต่ำลง)
//   - Chunking ให้ granularity ที่ดีกว่าในการ search
type Chunk struct {
	ID         string
	DocumentID string
	Title      string    // ชื่อเอกสารต้นฉบับ
	Source     string    // แหล่งที่มา
	Content    string    // ข้อความใน chunk
	ChunkIndex int       // ลำดับ chunk ในเอกสาร
	Embedding  []float64 // vector representation
}

// DocumentChunker — แบ่งเอกสารเป็น chunks
type DocumentChunker struct {
	MaxTokens int // ขนาดสูงสุดต่อ chunk (ประมาณ tokens)
	Overlap   int // จำนวน tokens ที่ overlap ระหว่าง chunks
}

func NewDocumentChunker(maxTokens, overlap int) *DocumentChunker {
	return &DocumentChunker{MaxTokens: maxTokens, Overlap: overlap}
}

// Chunk — แบ่งเอกสารเป็น chunks
// กลยุทธ์: แบ่งตาม paragraph ก่อน ถ้า paragraph ใหญ่เกิน ค่อยแบ่งต่อ
//
// ทำไมต้องแบ่งตาม paragraph?
//   - แต่ละ paragraph มักมีความหมายในตัวเอง (semantic unit)
//   - การตัดกลางประโยคทำให้ embedding คุณภาพต่ำ
//   - Retrieval ได้ context ที่สมบูรณ์กว่า
func (dc *DocumentChunker) Chunk(doc RawDocument) []Chunk {
	var chunks []Chunk

	// แบ่งตาม paragraph (double newline)
	paragraphs := strings.Split(doc.Content, "\n\n")

	var currentChunk strings.Builder
	currentTokens := 0
	chunkIndex := 0

	flushChunk := func() {
		text := strings.TrimSpace(currentChunk.String())
		if text == "" {
			return
		}

		chunkID := fmt.Sprintf("%s-chunk-%d", doc.ID, chunkIndex)
		chunks = append(chunks, Chunk{
			ID:         chunkID,
			DocumentID: doc.ID,
			Title:      doc.Title,
			Source:     doc.Source,
			Content:    text,
			ChunkIndex: chunkIndex,
		})

		chunkIndex++
		currentChunk.Reset()
		currentTokens = 0
	}

	for _, para := range paragraphs {
		para = strings.TrimSpace(para)
		if para == "" {
			continue
		}

		// ประมาณ token count: 1 token ≈ 4 chars สำหรับภาษาอังกฤษ
		// สำหรับภาษาไทย: 1 token ≈ 2-3 chars (ขึ้นกับ tokenizer)
		paraTokens := len(para) / 3 // ประมาณสำหรับ mixed Thai/English

		// ถ้า paragraph นี้จะทำให้ chunk ใหญ่เกิน → flush แล้วเริ่มใหม่
		if currentTokens+paraTokens > dc.MaxTokens && currentTokens > 0 {
			flushChunk()
		}

		// ถ้า paragraph เดียวใหญ่เกิน → แบ่งตามประโยค
		if paraTokens > dc.MaxTokens {
			sentences := splitSentences(para)
			for _, sentence := range sentences {
				sentTokens := len(sentence) / 3
				if currentTokens+sentTokens > dc.MaxTokens && currentTokens > 0 {
					flushChunk()
				}
				if currentChunk.Len() > 0 {
					currentChunk.WriteString(" ")
				}
				currentChunk.WriteString(sentence)
				currentTokens += sentTokens
			}
		} else {
			if currentChunk.Len() > 0 {
				currentChunk.WriteString("\n\n")
			}
			currentChunk.WriteString(para)
			currentTokens += paraTokens
		}
	}

	flushChunk() // flush chunk สุดท้าย

	return chunks
}

// splitSentences — แบ่งตามประโยค (ง่ายๆ)
func splitSentences(text string) []string {
	// แบ่งตาม . ! ? และ ฯ (Thai sentence terminator)
	var sentences []string
	var current strings.Builder

	for _, r := range text {
		current.WriteRune(r)
		if r == '.' || r == '!' || r == '?' || r == 'ฯ' {
			s := strings.TrimSpace(current.String())
			if s != "" {
				sentences = append(sentences, s)
			}
			current.Reset()
		}
	}

	if remaining := strings.TrimSpace(current.String()); remaining != "" {
		sentences = append(sentences, remaining)
	}

	return sentences
}

// ============================================================
// SECTION 2: Embeddings
// ============================================================
// Embedding = แปลงข้อความเป็น vector ตัวเลข
// ข้อความที่มีความหมายใกล้กัน จะมี vector ที่อยู่ใกล้กันใน space
//
// ใน production: ใช้ OpenAI text-embedding-3-small หรือ Cohere Embed
// ใน demo: ใช้ mock embedding ที่ deterministic

// EmbeddingClient — interface สำหรับ embedding model
type EmbeddingClient interface {
	Embed(ctx context.Context, texts []string) ([][]float64, error)
	Dimensions() int
}

// MockEmbeddingClient — สร้าง fake embeddings ที่ deterministic
// เทคนิค: ใช้ character frequencies เป็น features
// ข้อดี: เอกสารที่คล้ายกัน (มีคำเหมือนกัน) จะมี embedding ใกล้กัน
// ข้อเสีย: ไม่ได้เข้าใจความหมาย แต่เพียงพอสำหรับ demo
type MockEmbeddingClient struct {
	dims int
}

func NewMockEmbeddingClient(dims int) *MockEmbeddingClient {
	return &MockEmbeddingClient{dims: dims}
}

func (m *MockEmbeddingClient) Dimensions() int { return m.dims }

// Embed — สร้าง fake embeddings จาก text
// ใช้ SHA256 hash + character bigrams เป็น features
func (m *MockEmbeddingClient) Embed(_ context.Context, texts []string) ([][]float64, error) {
	embeddings := make([][]float64, len(texts))
	for i, text := range texts {
		embeddings[i] = m.embedText(text)
	}
	return embeddings, nil
}

func (m *MockEmbeddingClient) embedText(text string) []float64 {
	vec := make([]float64, m.dims)

	// Feature 1: Character frequencies (normalized)
	text = strings.ToLower(text)
	charCounts := make(map[rune]int)
	for _, r := range text {
		charCounts[r]++
	}
	total := len(text)
	if total == 0 {
		return vec
	}

	// Map character to dimension ด้วย hash
	for r, count := range charCounts {
		// ใช้ character value mod dims
		idx := int(r) % m.dims
		vec[idx] += float64(count) / float64(total)
	}

	// Feature 2: Word-based features (สำหรับ keyword matching)
	words := strings.Fields(text)
	for _, word := range words {
		// Hash word ไปยัง dimensions
		h := sha256.Sum256([]byte(word))
		for j := 0; j < 4 && j*2+1 < len(h); j++ {
			idx := (int(h[j*2])<<8 + int(h[j*2+1])) % m.dims
			vec[idx] += 0.1
		}
	}

	// Normalize เป็น unit vector (cosine similarity ต้องการ)
	return normalize(vec)
}

// ============================================================
// SECTION 3: Vector Store
// ============================================================
// Vector Store เก็บ embeddings และ search ด้วย similarity

// VectorStore — interface สำหรับ vector storage
type VectorStore interface {
	Add(ctx context.Context, chunks []Chunk) error
	Search(ctx context.Context, queryEmbedding []float64, topK int) ([]SearchResult, error)
	Count() int
}

// SearchResult — ผลการ search
type SearchResult struct {
	Chunk Chunk
	Score float64 // cosine similarity score (0-1, สูงกว่า = ใกล้กว่า)
}

// InMemoryVectorStore — vector store แบบ in-memory
// ใน production: ใช้ pgvector, Pinecone, Weaviate, ChromaDB
// In-memory เพียงพอสำหรับ demo และ testing
type InMemoryVectorStore struct {
	mu     sync.RWMutex
	chunks []Chunk
}

func NewInMemoryVectorStore() *InMemoryVectorStore {
	return &InMemoryVectorStore{}
}

func (vs *InMemoryVectorStore) Add(_ context.Context, chunks []Chunk) error {
	vs.mu.Lock()
	defer vs.mu.Unlock()
	vs.chunks = append(vs.chunks, chunks...)
	return nil
}

// Search — หา chunks ที่ใกล้เคียงที่สุดด้วย cosine similarity
// Cosine similarity = dot product / (||A|| * ||B||)
// ค่า 1.0 = เหมือนกัน, ค่า 0.0 = ไม่เกี่ยวกันเลย
//
// ทำไม cosine ไม่ใช่ Euclidean distance?
//   - Cosine ดูที่ "ทิศทาง" ของ vector ไม่ใช่ระยะห่าง
//   - เหมาะกับ text embeddings ที่ความยาว (magnitude) ไม่สำคัญ
//   - ข้อความยาว/สั้น ที่พูดเรื่องเดียวกัน จะมี cosine score สูง
func (vs *InMemoryVectorStore) Search(_ context.Context, queryEmbedding []float64, topK int) ([]SearchResult, error) {
	vs.mu.RLock()
	defer vs.mu.RUnlock()

	var results []SearchResult
	for _, chunk := range vs.chunks {
		if len(chunk.Embedding) == 0 {
			continue
		}
		score := cosineSimilarity(queryEmbedding, chunk.Embedding)
		results = append(results, SearchResult{Chunk: chunk, Score: score})
	}

	// เรียงจาก score สูงสุด
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	if topK < len(results) {
		results = results[:topK]
	}

	return results, nil
}

func (vs *InMemoryVectorStore) Count() int {
	vs.mu.RLock()
	defer vs.mu.RUnlock()
	return len(vs.chunks)
}

// ============================================================
// SECTION 4: Math utilities
// ============================================================

// cosineSimilarity — คำนวณ cosine similarity ระหว่าง 2 vectors
func cosineSimilarity(a, b []float64) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}

	var dotProduct, normA, normB float64
	for i := range a {
		dotProduct += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}

	if normA == 0 || normB == 0 {
		return 0
	}

	return dotProduct / (math.Sqrt(normA) * math.Sqrt(normB))
}

// normalize — แปลง vector เป็น unit vector
func normalize(vec []float64) []float64 {
	var norm float64
	for _, v := range vec {
		norm += v * v
	}
	norm = math.Sqrt(norm)

	if norm == 0 {
		return vec
	}

	result := make([]float64, len(vec))
	for i, v := range vec {
		result[i] = v / norm
	}
	return result
}

// ============================================================
// SECTION 5: Semantic Cache
// ============================================================
// Cache ที่ cache ตาม query embedding ไม่ใช่ exact string
// ข้อดี: query ที่ "หมายความเดียวกัน" แต่ใช้คำต่างกัน จะ hit cache
//
// ใน production จริง: ใช้ approximate nearest neighbor (ANN)
// ใน demo: linear scan ด้วย cosine threshold

// CacheEntry — entry ใน semantic cache
type CacheEntry struct {
	QueryEmbedding []float64
	Answer         string
	Sources        []string
	CreatedAt      time.Time
	TTL            time.Duration
}

// SemanticCache — cache ที่ match ด้วย semantic similarity
type SemanticCache struct {
	mu         sync.RWMutex
	entries    []*CacheEntry
	threshold  float64 // cosine similarity threshold สำหรับ cache hit
	defaultTTL time.Duration
	hits       int
	misses     int
}

func NewSemanticCache(threshold float64, defaultTTL time.Duration) *SemanticCache {
	return &SemanticCache{
		threshold:  threshold,
		defaultTTL: defaultTTL,
	}
}

// Get — หา answer จาก cache
// ถ้า query ใหม่มี cosine similarity >= threshold กับ cached query → cache hit
func (sc *SemanticCache) Get(queryEmbedding []float64) (string, []string, bool) {
	sc.mu.RLock()
	defer sc.mu.RUnlock()

	now := time.Now()
	for _, entry := range sc.entries {
		// ตรวจ TTL
		if now.After(entry.CreatedAt.Add(entry.TTL)) {
			continue // expired
		}

		// ตรวจ semantic similarity
		score := cosineSimilarity(queryEmbedding, entry.QueryEmbedding)
		if score >= sc.threshold {
			sc.hits++
			return entry.Answer, entry.Sources, true
		}
	}
	sc.misses++
	return "", nil, false
}

func (sc *SemanticCache) Set(queryEmbedding []float64, answer string, sources []string) {
	sc.mu.Lock()
	defer sc.mu.Unlock()

	sc.entries = append(sc.entries, &CacheEntry{
		QueryEmbedding: queryEmbedding,
		Answer:         answer,
		Sources:        sources,
		CreatedAt:      time.Now(),
		TTL:            sc.defaultTTL,
	})
}

func (sc *SemanticCache) Stats() (hits, misses int) {
	sc.mu.RLock()
	defer sc.mu.RUnlock()
	return sc.hits, sc.misses
}

// ============================================================
// SECTION 6: Mock LLM for RAG
// ============================================================

// RAGMockLLM — mock LLM สำหรับ RAG pipeline
type RAGMockLLM struct{}

// Generate — สร้าง answer จาก context chunks และ question
func (l *RAGMockLLM) Generate(ctx context.Context, question string, contextChunks []SearchResult) (string, error) {
	select {
	case <-time.After(80 * time.Millisecond):
	case <-ctx.Done():
		return "", ctx.Err()
	}

	if len(contextChunks) == 0 {
		return "ขออภัย ไม่พบข้อมูลที่เกี่ยวข้องในฐานข้อมูลของเรา", nil
	}

	// Mock: สร้าง answer โดยดึงข้อมูลจาก context
	var contextText strings.Builder
	for i, result := range contextChunks {
		if i >= 2 { // ใช้แค่ 2 chunks แรก
			break
		}
		contextText.WriteString(result.Chunk.Content[:min(200, len(result.Chunk.Content))])
		contextText.WriteString("... ")
	}

	// สร้าง answer จาก question
	answer := generateMockAnswer(question, contextChunks)
	return answer, nil
}

// generateMockAnswer — สร้าง mock answer ที่สมจริง
func generateMockAnswer(question string, results []SearchResult) string {
	// หาคำสำคัญจาก question
	questionLower := strings.ToLower(question)

	// ดึง source titles มาอ้างอิง
	var sources []string
	for _, r := range results {
		sources = append(sources, r.Chunk.Title)
	}

	// สร้าง answer ที่เหมาะสมตาม question
	switch {
	case strings.Contains(questionLower, "วันหยุด") || strings.Contains(questionLower, "ลา"):
		return fmt.Sprintf("ตามนโยบายบริษัท (อ้างอิง: %s) พนักงานมีสิทธิ์ลาพักร้อน 10 วันต่อปีสำหรับปีแรก และเพิ่มขึ้นเป็น 15 วันหลังทำงานครบ 3 ปี วันหยุดนักขัตฤกษ์ตามกฎหมายแรงงาน 13 วันต่อปี",
			strings.Join(sources[:min(2, len(sources))], ", "))

	case strings.Contains(questionLower, "ค่าใช้จ่าย") || strings.Contains(questionLower, "reimburs") || strings.Contains(questionLower, "เบิก"):
		return fmt.Sprintf("ตามนโยบาย (อ้างอิง: %s) พนักงานสามารถเบิกค่าใช้จ่ายในการเดินทางได้ตามจริงโดยต้องมีใบเสร็จ วงเงินค่าอาหารระหว่างเดินทาง 500 บาท/มื้อ ค่าโรงแรม 3,000 บาท/คืน ต้องส่งเอกสารภายใน 30 วัน",
			strings.Join(sources[:min(2, len(sources))], ", "))

	case strings.Contains(questionLower, "ประเมิน") || strings.Contains(questionLower, "performance") || strings.Contains(questionLower, "review"):
		return fmt.Sprintf("ตามนโยบาย (อ้างอิง: %s) การประเมินผลงานจะจัดขึ้นปีละ 2 ครั้ง ช่วงกลางปี (มิถุนายน) และปลายปี (ธันวาคม) โดยใช้ OKR และ 360-degree feedback ผลการประเมินส่งผลต่อการปรับเงินเดือนและ bonus",
			strings.Join(sources[:min(2, len(sources))], ", "))

	default:
		// Generic answer พร้อม source citation
		var contentPreviews []string
		for _, r := range results[:min(2, len(results))] {
			preview := r.Chunk.Content
			if len(preview) > 150 {
				preview = preview[:150] + "..."
			}
			contentPreviews = append(contentPreviews, preview)
		}

		return fmt.Sprintf("ข้อมูลที่เกี่ยวข้องกับคำถามของคุณ (จาก: %s):\n\n%s\n\n(หมายเหตุ: นี่คือ mock response สำหรับ demo)",
			strings.Join(sources[:min(2, len(sources))], ", "),
			strings.Join(contentPreviews, "\n\n"))
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ============================================================
// SECTION 7: RAG Pipeline
// ============================================================

// RAGPipeline — orchestrate ingest และ query
type RAGPipeline struct {
	chunker  *DocumentChunker
	embedder EmbeddingClient
	store    VectorStore
	llm      *RAGMockLLM
	cache    *SemanticCache
}

func NewRAGPipeline(embedDims int) *RAGPipeline {
	return &RAGPipeline{
		chunker:  NewDocumentChunker(500, 50),
		embedder: NewMockEmbeddingClient(embedDims),
		store:    NewInMemoryVectorStore(),
		llm:      &RAGMockLLM{},
		cache:    NewSemanticCache(0.92, 1*time.Hour), // 92% similarity = cache hit
	}
}

// Ingest — นำเอกสารเข้าระบบ
// Phase: document → chunk → embed → store
func (r *RAGPipeline) Ingest(ctx context.Context, docs []RawDocument) error {
	var allChunks []Chunk

	for _, doc := range docs {
		chunks := r.chunker.Chunk(doc)
		fmt.Printf("  [ingest] '%s' → %d chunks\n", doc.Title, len(chunks))
		allChunks = append(allChunks, chunks...)
	}

	// Embed ทุก chunks พร้อมกัน (batch)
	texts := make([]string, len(allChunks))
	for i, chunk := range allChunks {
		texts[i] = chunk.Content
	}

	embeddings, err := r.embedder.Embed(ctx, texts)
	if err != nil {
		return fmt.Errorf("embed chunks: %w", err)
	}

	// ใส่ embedding เข้า chunks
	for i := range allChunks {
		allChunks[i].Embedding = embeddings[i]
	}

	// เก็บใน vector store
	if err := r.store.Add(ctx, allChunks); err != nil {
		return fmt.Errorf("store chunks: %w", err)
	}

	fmt.Printf("  [ingest] total %d chunks stored\n", r.store.Count())
	return nil
}

// Query — ตอบคำถามด้วย RAG
// Phase: question → embed → search → retrieve → generate
// Returns: answer, sources, fromCache
func (r *RAGPipeline) Query(ctx context.Context, question string, topK int) (string, []string, bool, error) {
	// Embed query
	embeddings, err := r.embedder.Embed(ctx, []string{question})
	if err != nil {
		return "", nil, false, fmt.Errorf("embed query: %w", err)
	}
	queryEmbedding := embeddings[0]

	// ตรวจ semantic cache ก่อน
	if cached, sources, ok := r.cache.Get(queryEmbedding); ok {
		return cached, sources, true, nil
	}

	// Similarity search หา top-K chunks
	results, err := r.store.Search(ctx, queryEmbedding, topK)
	if err != nil {
		return "", nil, false, fmt.Errorf("search: %w", err)
	}

	// เก็บ sources สำหรับ citation
	sourcesMap := make(map[string]bool)
	for _, r := range results {
		sourceStr := fmt.Sprintf("%s (chunk %d)", r.Chunk.Title, r.Chunk.ChunkIndex+1)
		sourcesMap[sourceStr] = true
	}
	var sources []string
	for s := range sourcesMap {
		sources = append(sources, s)
	}

	// Generate answer ด้วย LLM
	answer, err := r.llm.Generate(ctx, question, results)
	if err != nil {
		return "", nil, false, fmt.Errorf("generate: %w", err)
	}

	// เก็บ cache
	r.cache.Set(queryEmbedding, answer, sources)

	return answer, sources, false, nil
}

// ============================================================
// SECTION 8: Sample Documents
// ============================================================
// เอกสารนโยบายบริษัทสำหรับ demo
// Context: บริษัทเทคโนโลยีขนาดกลางในไทย

var companyPolicyDocuments = []RawDocument{
	{
		ID:     "policy-hr-001",
		Title:  "นโยบายการลาและวันหยุด",
		Source: "hr-policy-leave-2024.pdf",
		Content: `นโยบายการลาและวันหยุดพักผ่อน บริษัท ดิจิทัลซอลูชั่น จำกัด
ฉบับปรับปรุง: 1 มกราคม 2567

วันหยุดนักขัตฤกษ์
บริษัทกำหนดวันหยุดนักขัตฤกษ์ตามกฎหมายแรงงาน 13 วันต่อปี รายละเอียดจะประกาศในต้นปีของทุกปี

การลาพักร้อน (Annual Leave)
พนักงานมีสิทธิ์ลาพักร้อนดังนี้:
- ปีที่ 1-2: 10 วันทำการต่อปี
- ปีที่ 3-5: 15 วันทำการต่อปี
- ปีที่ 6 ขึ้นไป: 20 วันทำการต่อปี
วันลาพักร้อนที่ไม่ได้ใช้สามารถสะสมได้ไม่เกิน 30 วัน ส่วนที่เกินจะถูกยกเลิก

การลาป่วย (Sick Leave)
พนักงานมีสิทธิ์ลาป่วยได้ 30 วันต่อปีโดยได้รับค่าจ้าง กรณีลาป่วยติดต่อกันเกิน 3 วัน ต้องแสดงใบรับรองแพทย์

การลาเพื่อดูแลครอบครัว (Family Care Leave)
พนักงานสามารถลาเพื่อดูแลบุตร คู่สมรส หรือบิดามารดาที่เจ็บป่วยได้ไม่เกิน 5 วันต่อปี

การลาคลอด (Maternity Leave)
พนักงานหญิงมีสิทธิ์ลาคลอด 98 วัน โดยได้รับค่าจ้าง 45 วันแรก

ขั้นตอนการขอลา
1. ยื่นขอลาผ่านระบบ HR Online ล่วงหน้าอย่างน้อย 3 วันทำการ (ยกเว้นลาป่วย)
2. รอการอนุมัติจากผู้บังคับบัญชาโดยตรง
3. กรณีฉุกเฉิน แจ้งผู้บังคับบัญชาทางโทรศัพท์และยื่นเอกสารภายหลัง`,
	},
	{
		ID:     "policy-finance-001",
		Title:  "นโยบายค่าใช้จ่ายและการเบิกจ่าย",
		Source: "finance-policy-expense-2024.pdf",
		Content: `นโยบายค่าใช้จ่ายและการเบิกจ่าย

การเดินทางในประเทศ
ค่าเดินทางโดยเครื่องบิน: ชั้น Economy เท่านั้น เบิกได้ตามจริงพร้อมใบเสร็จ
ค่าโรงแรม: ไม่เกิน 3,000 บาท/คืน (กรุงเทพฯ) หรือ 2,000 บาท/คืน (ต่างจังหวัด)
ค่าอาหาร: ไม่เกิน 500 บาท/มื้อ หรือ 1,500 บาท/วัน
ค่าเดินทางในเมือง: เบิกได้ตามจริง (Grab, แท็กซี่) พร้อมใบเสร็จ หรือ 3.50 บาท/กม. สำหรับรถส่วนตัว

การเดินทางต่างประเทศ
ต้องได้รับอนุมัติจาก VP ขึ้นไปก่อนเดินทาง
ค่าตั๋วเครื่องบิน: Business Class สำหรับ Director+ หรือบินนานกว่า 6 ชั่วโมง
ค่าโรงแรม: ไม่เกิน 150 USD/คืน
เบี้ยเลี้ยงต่างประเทศ: 75 USD/วัน

ค่าใช้จ่ายด้าน IT
Software licenses: ต้องได้รับอนุมัติจาก IT Manager ก่อนซื้อ
Hardware: ต้องผ่านขั้นตอน procurement ปกติ
Cloud services: สูงสุด 5,000 บาท/เดือน อนุมัติโดย Tech Lead

ขั้นตอนการเบิกจ่าย
1. รวบรวมใบเสร็จและเอกสารประกอบ
2. กรอกแบบฟอร์ม Expense Claim ในระบบ ERP
3. แนบใบเสร็จ (สแกน/ถ่ายภาพ)
4. ส่งให้ผู้บังคับบัญชาอนุมัติ
5. ส่งเอกสารต้นฉบับให้ฝ่ายการเงินภายใน 30 วัน
6. ฝ่ายการเงินโอนเงินภายใน 7 วันทำการหลังได้รับเอกสารครบ`,
	},
	{
		ID:     "policy-it-001",
		Title:  "นโยบายความปลอดภัยด้านสารสนเทศ",
		Source: "it-security-policy-2024.pdf",
		Content: `นโยบายความปลอดภัยด้านสารสนเทศ (Information Security Policy)
ISO 27001 Compliant | อัปเดต: มีนาคม 2567

Password Policy
- รหัสผ่านต้องยาวอย่างน้อย 12 ตัวอักษร
- ต้องมีตัวพิมพ์ใหญ่ ตัวพิมพ์เล็ก ตัวเลข และอักขระพิเศษ
- เปลี่ยนรหัสผ่านทุก 90 วัน
- ห้ามใช้รหัสผ่านซ้ำกับ 12 ครั้งที่ผ่านมา
- บังคับใช้ MFA สำหรับทุกระบบ

การใช้อุปกรณ์ส่วนตัว (BYOD Policy)
- อนุญาตให้ใช้สมาร์ทโฟนส่วนตัวสำหรับ email และ Teams
- ต้องติดตั้ง MDM (Mobile Device Management) ก่อนใช้งาน
- ห้ามเก็บข้อมูลลูกค้าในอุปกรณ์ส่วนตัว
- บริษัทสามารถ remote wipe ข้อมูลบริษัทจากอุปกรณ์ได้

การจัดการข้อมูลลับ
ข้อมูลชั้น Confidential: ข้อมูลลูกค้า, รหัสซอฟต์แวร์, แผนธุรกิจ
- ห้าม share ทาง email ส่วนตัว
- ต้องเข้ารหัสก่อนส่งผ่านระบบ
ข้อมูลชั้น Internal: เอกสาร HR, นโยบายภายใน
- ใช้ได้ภายในองค์กรเท่านั้น

Incident Response
หากพบเหตุผิดปกติ ให้แจ้ง:
1. IT Security Team: security@company.co.th หรือ ext. 5500
2. ห้ามพยายามแก้ไขด้วยตนเอง
3. เก็บหลักฐาน (screenshot, log) ไว้ก่อน`,
	},
	{
		ID:     "policy-hr-002",
		Title:  "นโยบายการประเมินผลและพัฒนาบุคลากร",
		Source: "hr-policy-performance-2024.pdf",
		Content: `นโยบายการประเมินผลการปฏิบัติงานและพัฒนาบุคลากร

รอบการประเมิน
บริษัทดำเนินการประเมินผล 2 ครั้งต่อปี:
- Mid-Year Review: มิถุนายน (ประเมินผลครึ่งปีแรก วางแผนครึ่งปีหลัง)
- Year-End Review: ธันวาคม (ประเมินผลเต็มปี กำหนด KPI ปีถัดไป)

กระบวนการประเมิน
1. Self-Assessment: พนักงานประเมินตนเองในระบบ
2. Manager Review: ผู้บังคับบัญชาให้ feedback และ rating
3. 360-Degree Feedback: เพื่อนร่วมงานและทีมให้ feedback (สำหรับ Senior+)
4. Calibration: HR และ Management calibrate ratings ให้ consistent
5. Feedback Meeting: พบกันระหว่างพนักงานและผู้บังคับบัญชา

Rating Scale (1-5):
5 = Exceptional: ผลงานเกินคาดหมายอย่างมีนัยสำคัญ
4 = Exceeds: ผลงานสูงกว่า expectation
3 = Meets: ผลงานตาม expectation
2 = Needs Improvement: ต้องปรับปรุง มีแผน PIP
1 = Unsatisfactory: ไม่ผ่านเกณฑ์

ผลต่อค่าตอบแทน
Rating 5: ปรับเงินเดือน 8-12% + Bonus 20-25% ของเงินเดือนปีต่อปี
Rating 4: ปรับเงินเดือน 5-8% + Bonus 15-20%
Rating 3: ปรับเงินเดือน 3-5% + Bonus 10-15%
Rating 2: ปรับเงินเดือน 0-3% ไม่มี Bonus อยู่ใน PIP
Rating 1: ไม่ปรับเงินเดือน ไม่มี Bonus พิจารณาเลิกจ้าง

การพัฒนาบุคลากร
งบประมาณฝึกอบรม 20,000 บาท/คน/ปี สำหรับ rating 3+
การสนับสนุนการศึกษาต่อ: Degree ปริญญาโท 50% ค่าเล่าเรียน สูงสุด 100,000 บาท/ปี`,
	},
	{
		ID:     "policy-ops-001",
		Title:  "นโยบายการทำงานแบบไฮบริดและ Remote Work",
		Source: "ops-policy-hybrid-work-2024.pdf",
		Content: `นโยบายการทำงานแบบ Hybrid Work
มีผลตั้งแต่ 1 มกราคม 2567

โมเดล Hybrid Work
บริษัทใช้โมเดล 3-2: ทำงานที่ออฟฟิศ 3 วัน และ Work from Home 2 วันต่อสัปดาห์
วัน Core Day (ต้องมาออฟฟิศ): อังคาร พุธ พฤหัสบดี
วัน Flexible (ออฟฟิศหรือ WFH): จันทร์ และ ศุกร์

เงื่อนไข Work from Home
1. มี Internet ความเร็วอย่างน้อย 30 Mbps
2. มีพื้นที่ทำงานที่เหมาะสม ไม่มีเสียงรบกวน
3. ออนไลน์และตอบ message ภายใน 15 นาที ระหว่าง 9:00-18:00 น.
4. เข้าร่วม Video call ได้ตลอดเวลาทำงาน
5. ห้ามทำงานจากต่างประเทศโดยไม่แจ้ง HR (มีผลทางภาษีและกฎหมายแรงงาน)

Workation Policy
พนักงานสามารถ Work from Anywhere ในประเทศได้ไม่เกิน 30 วัน/ปี
ต้องแจ้ง Manager และ HR ล่วงหน้า 1 สัปดาห์

อุปกรณ์และสวัสดิการ WFH
บริษัทจัดให้: MacBook หรือ Windows Laptop, External Monitor 1 จอ
เงินช่วยเหลือ: 2,000 บาท/เดือน สำหรับ Internet และ Electricity (สำหรับพนักงานที่ WFH)

Core Hours และ Flexibility
Core hours: 10:00-16:00 น. (ต้องพร้อมทำงาน)
เริ่มงานได้ตั้งแต่ 7:00-10:00 น. และเลิกงานตาม flexible schedule
ชั่วโมงทำงานขั้นต่ำ 8 ชั่วโมง/วัน ไม่นับชั่วโมงพัก`,
	},
}

// ============================================================
// SECTION 9: Main Demo
// ============================================================

func main() {
	fmt.Println("=== Module 7: RAG System Demo ===")
	fmt.Println("(ใช้ Mock Embeddings และ Mock LLM — ไม่ต้องใช้ API key จริง)")
	fmt.Println()

	ctx := context.Background()
	rag := NewRAGPipeline(128) // 128-dimension embeddings

	// ─── Phase 1: Ingest documents ──────────────────────────
	fmt.Println("=== Phase 1: Ingesting Documents ===")
	start := time.Now()
	if err := rag.Ingest(ctx, companyPolicyDocuments); err != nil {
		log.Fatalf("ingest error: %v", err)
	}
	fmt.Printf("Ingest completed in %v\n\n", time.Since(start))

	// ─── Phase 2: Query ──────────────────────────────────────
	fmt.Println("=== Phase 2: Answering Questions ===")

	queries := []struct {
		question string
		desc     string
	}{
		{
			question: "พนักงานมีสิทธิ์ลาพักร้อนกี่วันต่อปี?",
			desc:     "Q1: วันลาพักร้อน",
		},
		{
			question: "เบิกค่าโรงแรมได้เท่าไหร่เวลาเดินทางไปต่างจังหวัด?",
			desc:     "Q2: ค่าใช้จ่ายการเดินทาง",
		},
		{
			question: "การประเมินผลงานใช้เกณฑ์อะไรและส่งผลต่อเงินเดือนอย่างไร?",
			desc:     "Q3: การประเมินผลงาน",
		},
		{
			question: "พนักงานมีสิทธิ์ลาพักร้อนกี่วัน?", // คำถามเดิมในรูปแบบต่าง → test cache
			desc:     "Q4: วันลาพักร้อน (ซ้ำ → ควร cache hit)",
		},
		{
			question: "พนักงานมีสิทธิ์ Work From Home อย่างไร?",
			desc:     "Q5: สิทธิ์ WFH",
		},
	}

	for _, q := range queries {
		fmt.Printf("--- %s ---\n", q.desc)
		fmt.Printf("คำถาม: %s\n", q.question)

		qStart := time.Now()
		answer, sources, fromCache, err := rag.Query(ctx, q.question, 3)
		qElapsed := time.Since(qStart)

		if err != nil {
			fmt.Printf("Error: %v\n\n", err)
			continue
		}

		cacheStr := ""
		if fromCache {
			cacheStr = " [CACHE HIT]"
		}

		fmt.Printf("คำตอบ%s: %s\n", cacheStr, answer)
		fmt.Printf("แหล่งอ้างอิง: %s\n", strings.Join(sources, "; "))
		fmt.Printf("เวลาที่ใช้: %v\n\n", qElapsed)
	}

	// ─── Phase 3: Statistics ─────────────────────────────────
	fmt.Println("=== Phase 3: System Statistics ===")
	fmt.Printf("Vector store: %d chunks\n", rag.store.Count())

	cacheHits, cacheMisses := rag.cache.Stats()
	fmt.Printf("Semantic cache: %d hits, %d misses\n", cacheHits, cacheMisses)
	if cacheHits+cacheMisses > 0 {
		fmt.Printf("Cache hit rate: %.1f%%\n", float64(cacheHits)/float64(cacheHits+cacheMisses)*100)
	}

	// ─── Phase 4: Explain RAG components ─────────────────────
	fmt.Println("\n=== RAG Components Summary ===")
	fmt.Println("1. Chunker:        แบ่งเอกสารตาม paragraph, max 500 tokens")
	fmt.Println("2. Embedder:       Mock 128-dim embeddings (production: OpenAI/Cohere)")
	fmt.Println("3. VectorStore:    In-memory cosine similarity search")
	fmt.Println("4. SemanticCache:  92% similarity threshold, 1 hour TTL")
	fmt.Println("5. LLM:            Mock generator (production: GPT-4o/Claude)")
	fmt.Println("6. Citations:      แสดง source document และ chunk index")
}
