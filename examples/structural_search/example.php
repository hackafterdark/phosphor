<?php

/**
 * Config holds application configuration.
 */
class Config {
    public string $host;
    public int $port;
    public bool $debug;

    public function __construct(string $host, int $port, bool $debug) {
        $this->host = $host;
        $this->port = $port;
        $this->debug = $debug;
    }

    /**
     * Get host.
     */
    public function getHost(): string {
        return $this->host;
    }

    /**
     * Get port.
     */
    public function getPort(): int {
        return $this->port;
    }
}

/**
 * Person represents a person.
 */
class Person {
    public string $name;
    public int $age;

    public function __construct(string $name, int $age) {
        $this->name = $name;
        $this->age = $age;
    }

    /**
     * Greet the person.
     */
    public function greet(): string {
        return "Hello, I am {$this->name}";
    }
}

/**
 * PersonService manages persons.
 */
class PersonService {
    /**
     * @var Person[]
     */
    private array $persons = [];

    /**
     * Add a person.
     */
    public function addPerson(Person $person): void {
        $this->persons[] = $person;
    }

    /**
     * Get all persons.
     * @return Person[]
     */
    public function getAllPersons(): array {
        return $this->persons;
    }

    /**
     * Get person count.
     */
    public function getPersonCount(): int {
        return count($this->persons);
    }
}

/**
 * Create a config with defaults.
 */
function createConfig(): Config {
    return new Config("localhost", 3000, false);
}

/**
 * Greet a person.
 */
function greetPerson(Person $person): string {
    return $person->greet() . "!";
}

// Main execution.
$config = createConfig();
echo "Starting at {$config->getHost()}:{$config->getPort()}\n";

$service = new PersonService();
$service->addPerson(new Person("Alice", 30));
$service->addPerson(new Person("Bob", 25));

foreach ($service->getAllPersons() as $person) {
    echo greetPerson($person) . "\n";
}

echo "Total persons: " . $service->getPersonCount() . "\n";
