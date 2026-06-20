// Java example for structural search testing.

/**
 * Config holds application configuration.
 */
class Config {
    private String host;
    private int port;
    private boolean debug;

    public Config() {
        this.host = "localhost";
        this.port = 8080;
        this.debug = false;
    }

    public String getUrl() {
        return "http://" + host + ":" + port;
    }

    public String getHost() { return host; }
    public int getPort() { return port; }
    public boolean isDebug() { return debug; }
}

/**
 * Person represents a person.
 */
class Person {
    private String name;
    private int age;

    public Person(String name, int age) {
        this.name = name;
        this.age = age;
    }

    public String greet() {
        return "Hello, I'm " + name + " and I'm " + age + " years old.";
    }

    public String getName() { return name; }
    public int getAge() { return age; }
}

/**
 * Logger provides logging functionality.
 */
class Logger {
    public static void info(String message) {
        System.out.println("[INFO] " + message);
    }

    public static void error(String message) {
        System.err.println("[ERROR] " + message);
    }
}

/**
 * PersonService manages persons.
 */
class PersonService {
    private java.util.List<Person> persons;

    public PersonService() {
        this.persons = new java.util.ArrayList<>();
    }

    public void addPerson(String name, int age) {
        Person person = new Person(name, age);
        persons.add(person);
        Logger.info("Added person: " + name);
    }

    public java.util.List<Person> getAllPersons() {
        return persons;
    }

    public int getPersonCount() {
        return persons.size();
    }
}

/**
 * Main application entry point.
 */
public class Example {
    public static void main(String[] args) {
        Config config = new Config();
        Logger.info("Starting application at " + config.getUrl());

        PersonService service = new PersonService();
        service.addPerson("Alice", 30);
        service.addPerson("Bob", 25);
        service.addPerson("Charlie", 35);

        for (Person person : service.getAllPersons()) {
            System.out.println(person.greet());
        }

        Logger.info("Total persons: " + service.getPersonCount());
    }
}
