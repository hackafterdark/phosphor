#include "common/scanner.h"
#include <stdio.h>

void *tree_sitter_typescript_external_scanner_create() { return NULL; }

void tree_sitter_typescript_external_scanner_destroy(void *payload) {}

unsigned tree_sitter_typescript_external_scanner_serialize(void *payload, char *buffer) { return 0; }

void tree_sitter_typescript_external_scanner_deserialize(void *payload, const char *buffer, unsigned length) {}

bool tree_sitter_typescript_external_scanner_scan(void *payload, TSLexer *lexer, const bool *valid_symbols) {
    FILE *f = fopen("F:/hackafterdark/phosphor/debug_scanner_calls.txt", "a");
    if (f) {
        fprintf(f, "Called scanner. TEMPLATE_CHARS valid: %d, lookahead: %d\n", valid_symbols[TEMPLATE_CHARS], lexer->lookahead);
        fclose(f);
    }
    bool res = external_scanner_scan(payload, lexer, valid_symbols);
    f = fopen("F:/hackafterdark/phosphor/debug_scanner_calls.txt", "a");
    if (f) {
        fprintf(f, "Returned: %d\n", res);
        fclose(f);
    }
    return res;
}
