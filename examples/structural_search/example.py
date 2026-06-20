# Python example for structural search testing.

"""
Example Python module for structural search testing.
"""

import os
import sys
from typing import List, Optional


class Config:
    """Application configuration."""

    def __init__(self):
        self.host: str = "localhost"
        self.port: int = 8080
        self.debug: bool = False

    def get_url(self) -> str:
        """Get the application URL."""
        return f"http://{self.host}:{self.port}"


class Person:
    """Represents a person."""

    def __init__(self, name: str, age: int):
        self.name = name
        self.age = age

    def greet(self) -> str:
        """Return a greeting string."""
        return f"Hello, I'm {self.name} and I'm {self.age} years old."

    def __repr__(self) -> str:
        return f"Person(name={self.name!r}, age={self.age})"


class Logger:
    """Provides logging functionality."""

    @staticmethod
    def info(message: str) -> None:
        """Log an info message."""
        print(f"[INFO] {message}")

    @staticmethod
    def error(message: str) -> None:
        """Log an error message."""
        print(f"[ERROR] {message}", file=sys.stderr)


class PersonService:
    """Manages persons."""

    def __init__(self):
        self._persons: List[Person] = []

    def add_person(self, name: str, age: int) -> Person:
        """Add a person and log it."""
        person = Person(name, age)
        self._persons.append(person)
        Logger.info(f"Added person: {name}")
        return person

    def get_all_persons(self) -> List[Person]:
        """Return all persons."""
        return list(self._persons)

    def get_person_count(self) -> int:
        """Return the number of persons."""
        return len(self._persons)

    def get_persons_by_age(self, min_age: int, max_age: int) -> List[Person]:
        """Filter persons by age range."""
        return [p for p in self._persons if min_age <= p.age <= max_age]


def main() -> None:
    """Main entry point."""
    config = Config()
    Logger.info(f"Starting application at {config.get_url()}")

    service = PersonService()
    service.add_person("Alice", 30)
    service.add_person("Bob", 25)
    service.add_person("Charlie", 35)

    for person in service.get_all_persons():
        print(person.greet())

    Logger.info(f"Total persons: {service.get_person_count()}")

    # Debug mode check.
    if os.environ.get("DEBUG"):
        Logger.info("Debug mode enabled")


if __name__ == "__main__":
    main()
