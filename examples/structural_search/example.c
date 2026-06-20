#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#define MAX_NAME_LEN 64

// Config holds application settings.
typedef struct {
    char host[MAX_NAME_LEN];
    int port;
    int verbose;
} Config;

// Person represents a person with a name and age.
typedef struct {
    char name[MAX_NAME_LEN];
    int age;
} Person;

// init_config sets default configuration values.
void init_config(Config *cfg) {
    strcpy(cfg->host, "localhost");
    cfg->port = 8080;
    cfg->verbose = 0;
}

// print_person prints a Person's details.
void print_person(const Person *p) {
    printf("Name: %s, Age: %d\n", p->name, p->age);
}

// compare_persons is a comparison function for qsort.
int compare_persons(const void *a, const void *b) {
    const Person *pa = (const Person *)a;
    const Person *pb = (const Person *)b;
    return pa->age - pb->age;
}

int main(void) {
    Config cfg;
    init_config(&cfg);

    Person people[] = {
        {"Alice", 30},
        {"Bob", 25},
        {"Charlie", 35},
    };
    int n = sizeof(people) / sizeof(people[0]);

    qsort(people, n, sizeof(Person), compare_persons);

    for (int i = 0; i < n; i++) {
        print_person(&people[i]);
    }

    return 0;
}
