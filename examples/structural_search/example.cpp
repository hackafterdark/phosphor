#include <iostream>
#include <string>
#include <vector>
#include <memory>

// Config holds application settings.
class Config {
public:
    std::string host;
    int port;
    bool verbose;

    Config() : host("localhost"), port(8080), verbose(false) {}
};

// Person represents a person.
class Person {
public:
    std::string name;
    int age;

    Person(const std::string& name, int age) : name(name), age(age) {}

    void print() const {
        std::cout << "Name: " << name << ", Age: " << age << std::endl;
    }
};

// Logger provides logging functionality.
class Logger {
public:
    static void info(const std::string& msg) {
        std::cout << "[INFO] " << msg << std::endl;
    }

    static void error(const std::string& msg) {
        std::cerr << "[ERROR] " << msg << std::endl;
    }
};

// create_persons creates a vector of persons.
std::vector<std::shared_ptr<Person>> create_persons() {
    return {
        std::make_shared<Person>("Alice", 30),
        std::make_shared<Person>("Bob", 25),
        std::make_shared<Person>("Charlie", 35),
    };
}

int main() {
    Config cfg;
    Logger::info("Starting application");

    auto persons = create_persons();
    for (const auto& p : persons) {
        p->print();
    }

    return 0;
}