use std::io;
use std::collections::HashMap;

/// Config holds the application configuration.
struct Config {
    host: String,
    port: u32,
    debug: bool,
}

/// Printable trait for objects that can print themselves.
trait Printable {
    fn print(&self);
}

/// Person represents a person.
#[derive(Clone)]
struct Person {
    name: String,
    age: u32,
}

impl Printable for Person {
    fn print(&self) {
        println!("{}", self.greet());
    }
}

impl Person {
    /// Create a new Person.
    fn new(name: &str, age: u32) -> Self {
        Person {
            name: name.to_string(),
            age,
        }
    }

    /// Greet the person.
    fn greet(&self) -> String {
        format!("Hello, I'm {} and I'm {} years old.", self.name, self.age)
    }

    /// Check if the person is an adult, returning an error if they are minor.
    fn check_adult(&self) -> Result<(), String> {
        if self.age < 18 {
            return Err("Too young".to_string());
        }
        Ok(())
    }
}

/// Logger provides logging functionality.
struct Logger;

impl Logger {
    /// Log an info message.
    fn info(message: &str) {
        println!("[INFO] {}", message);
    }

    /// Log an error message.
    fn error(message: &str) {
        eprintln!("[ERROR] {}", message);
    }
}

/// PersonService manages persons.
struct PersonService {
    persons: Vec<Person>,
}

impl PersonService {
    /// Create a new PersonService.
    fn new() -> Self {
        PersonService {
            persons: Vec::new(),
        }
    }

    /// Add a person.
    fn add_person(&mut self, name: &str, age: u32) -> Person {
        let person = Person::new(name, age);
        self.persons.push(person.clone());
        Logger::info(&format!("Added person: {}", name));
        person
    }

    /// Get all persons.
    fn get_all_persons(&self) -> &[Person] {
        &self.persons
    }

    /// Get the person count.
    fn get_person_count(&self) -> usize {
        self.persons.len()
    }

    /// Sort persons by age.
    fn sort_by_age(&mut self) {
        self.persons.sort_by_key(|p| p.age);
    }
}

/// Main entry point.
fn main() {
    Logger::info("Starting application");

    let mut service = PersonService::new();
    service.add_person("Alice", 30);
    service.add_person("Bob", 25);
    service.add_person("Charlie", 35);

    service.sort_by_age();

    for person in service.get_all_persons() {
        println!("{}", person.greet());
    }

    Logger::info(&format!("Total persons: {}", service.get_person_count()));
}
