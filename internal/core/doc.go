// Package core is the dialect-neutral middle of oocla: the conversation
// model, the model catalog, history flattening, session mapping, and the
// engine that runs one turn against a Generator.
//
// The Ollama dialect (internal/ollama) and the OpenAI dialect
// (internal/openai) both convert their wire formats into these types and
// hand them to the Engine. Neither dialect imports the other; core is the
// only thing they share.
package core
