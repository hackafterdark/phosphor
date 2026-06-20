using System;
using System.Collections.Generic;
using System.Linq;

namespace Example {

    // Config holds application settings.
    public class Config {
        public string Host { get; set; } = "localhost";
        public int Port { get; set; } = 8080;
        public bool Verbose { get; set; }
    }

    // Person represents a person.
    public class Person {
        public string Name { get; set; }
        public int Age { get; set; }

        public Person(string name, int age) {
            Name = name;
            Age = age;
        }

        public void Print() {
            Console.WriteLine($"Name: {Name}, Age: {Age}");
        }
    }

    // Logger provides logging functionality.
    public static class Logger {
        public static void Info(string message) {
            Console.WriteLine($"[INFO] {message}");
        }

        public static void Error(string message) {
            Console.Error.WriteLine($"[ERROR] {message}");
        }
    }

    // Program is the entry point.
    public class Program {
        public static List<Person> CreatePersons() {
            return new List<Person> {
                new Person("Alice", 30),
                new Person("Bob", 25),
                new Person("Charlie", 35),
            };
        }

        public static void Main(string[] args) {
            var config = new Config();
            Logger.Info("Starting application");

            var persons = CreatePersons();
            foreach (var person in persons) {
                person.Print();
            }

            var sorted = persons.OrderBy(p => p.Age).ToList();
            Logger.Info($"Sorted {sorted.Count} persons");
        }
    }
}
