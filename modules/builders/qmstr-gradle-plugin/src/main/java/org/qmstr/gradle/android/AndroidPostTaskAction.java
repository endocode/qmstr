package org.qmstr.gradle.android;

import static org.qmstr.util.transformations.MergeDexTransformation.wrapFind;

import java.io.File;
import java.io.FileNotFoundException;
import java.io.FileReader;
import java.io.IOException;
import java.nio.file.Path;
import java.nio.file.Paths;
import java.util.Collections;
import java.util.Optional;
import java.util.Scanner;
import java.util.Set;
import java.util.jar.JarFile;
import java.util.regex.Matcher;
import java.util.regex.Pattern;
import java.util.stream.Collectors;

import org.gradle.api.Project;
import org.gradle.api.Task;
import org.gradle.api.plugins.AppliedPlugin;
import org.qmstr.gradle.ResultUnavailableException;
import org.qmstr.grpc.service.Datamodel;
import org.qmstr.grpc.service.Datamodel.PackageNode;
import org.qmstr.util.FilenodeUtils;
import org.qmstr.util.PackagenodeUtils;
import org.qmstr.util.transformations.Transform;
import org.qmstr.util.transformations.TransformationException;

public class AndroidPostTaskAction extends AndroidTaskAction {

    public AndroidPostTaskAction(Project project, AppliedPlugin plugin) {
        super(project, plugin);
    }

    // This method tries to guess the root of the package hierarchy
    // The compile task will put the package hierarchy in a classes dir. So this method will try to step up until it finds a classes dir.
    // Beware that this is nothing but stupid and only for the PoC.
    private File guessSourcePath(File sourceFile) {
        if (sourceFile.getParentFile() == null || sourceFile.getParentFile().toPath().getFileName() == null) {
            return sourceFile;
        }
        if (sourceFile.getParentFile().toPath().getFileName().toString().equals("classes")) {
            return sourceFile.getParentFile();
        }
        return guessSourcePath(sourceFile.getParentFile());
    }
    
    private void handleDex(Task task) {
        task.getInputs().getFiles().forEach(sf -> {
            Set<Datamodel.FileNode> nodes;
            try {
                // Here it becomes ugly. The output dir we get from the task is not yet the dir to look for the package file tree that holds the dex files.
                // There is another dir in the hierarchy. This might be due to multidex https://developer.android.com/studio/build/multidex
                // Anyway for now we just assume that there is one more dir called '0'
                Set<File> outdirs = task.getOutputs().getFiles().getFiles().stream()
                        .map(f -> f.toPath().resolve("0").toFile()).collect(Collectors.toSet());

                // Here it gets even uglier. The processSourceFile method assumes you have a source file and a set of input directories where your sources (inside a package hierarchy) reside.
                // This however is not the case here because we are not working with sourcesets like in a Java build.
                // Therefore we need to find the root of the package hierarchy from the filename. This is what the brain-damaged guessSourcePath method does.
                nodes = FilenodeUtils.processSourceFiles(Transform.DEXCLASS, Collections.singleton(sf),
                        Collections.singleton(guessSourcePath(sf)), outdirs);
                if (!nodes.isEmpty()) {
                    bsc.SendBuildFileNodes(nodes);
                } else {
                    bsc.SendLogMessage(String.format("No filenodes after processing %s", sf.getName()));
                }
            } catch (TransformationException e) {
                task.getLogger().warn("{} failed: {}", this.getClass().getName(), e.getMessage());
            } catch (FileNotFoundException fnfe) {
                task.getLogger().warn("{} failed: {}", this.getClass().getName(), fnfe.getMessage());
            }

        });
    }

    private void handleCompileJava(Task task) {
        Set<File> sourceDirs = getSourceDirs(task.getProject());
        Set<File> outDirs = task.getOutputs().getFiles().getFiles();

        task.getInputs().getFiles().forEach(sf -> {
            Set<Datamodel.FileNode> nodes;
            try {
                nodes = FilenodeUtils.processSourceFiles(Transform.COMPILEJAVA, Collections.singleton(sf), sourceDirs, outDirs);
                if (!nodes.isEmpty()) {
                    bsc.SendBuildFileNodes(nodes);
                } else {
                    bsc.SendLogMessage(String.format("No filenodes after processing %s", sf.getName()));
                }
            } catch (TransformationException e) {
                task.getLogger().warn("{} failed: {}", this.getClass().getName(), e.getMessage());
            } catch (FileNotFoundException fnfe) {
                task.getLogger().warn("{} failed: {}", this.getClass().getName(), fnfe.getMessage());
            }

        });
    }

    private void handleMergeDex(Task task) throws ResultUnavailableException {
        Set<File> classesDexDirs = task.getOutputs().getFiles().getFiles();
        Set<File> inputDirs = task.getInputs().getFiles().getFiles();
        
        Set<Datamodel.FileNode> nodes;
        try {
            nodes = FilenodeUtils.processSourceFiles(Transform.MERGEDEX, Collections.emptySet(), inputDirs, classesDexDirs);
            if (!nodes.isEmpty()) {
                bsc.SendBuildFileNodes(nodes);
            } else {
                bsc.SendLogMessage(String.format("No filenodes after processing %s", inputDirs));
            }
        } catch (TransformationException e) {
            task.getLogger().warn("{} failed: {}", this.getClass().getName(), e.getMessage());
        } catch (FileNotFoundException fnfe) {
            task.getLogger().warn("{} failed: {}", this.getClass().getName(), fnfe.getMessage());
        }
    } 

    @Override
    public void execute(Task task) {
        if (debug) {
            logTaskInputOutput(task);
        }

        switch (SimpleTask.detectTask(task.getName())) {
            case COMPILEJAVA:
                handleCompileJava(task);
                break;
            case DEX:
                handleDex(task);
                break;
            case MERGEDEX:
                try {
                    handleMergeDex(task);
                } catch (ResultUnavailableException e) {
                    task.getLogger().error("Failed in merge dex: {}", e.getMessage());
                }
                break;
            case PACKAGEAPK:
                try {
                    handleApk(task);
                } catch (ResultUnavailableException e) {
                    task.getLogger().error("Failed in packaging apk: {}", e.getMessage());
                }
                break;
            case PACKAGEFULLJAR:
                handleJar(task, Transform.PACKAGEFULLJAR);
            case PACKAGECLASSESJAR:
                handleJar(task, Transform.PACKAGECLASSESJAR);
                break;
            default:
                break;
        }
    }

    private void handleJar(Task task, Transform jarTransform) {
        Set<File> outFiles = task.getOutputs().getFiles().getFiles();
        Set<File> inputFiles = task.getInputs().getFiles().getFiles();
        
        Set<Datamodel.FileNode> nodes;
        try {
            nodes = FilenodeUtils.processSourceFiles(jarTransform, Collections.emptySet(), inputFiles, outFiles);
            if (!nodes.isEmpty()) {
                bsc.SendBuildFileNodes(nodes);
            } else {
                String message = String.format("No filenodes after processing %s", inputFiles);
                bsc.SendLogMessage(message);
                task.getLogger().warn(message);
            }
        } catch (TransformationException e) {
            task.getLogger().warn("{} failed: {}", this.getClass().getName(), e.getMessage());
        } catch (FileNotFoundException fnfe) {
            task.getLogger().warn("{} failed: {}", this.getClass().getName(), fnfe.getMessage());
        }
    }

    private void handleApk(Task task) throws ResultUnavailableException {
        Set<File> outDirs = task.getOutputs().getFiles().getFiles();
        Set<File> inputDirs = task.getInputs().getFiles().getFiles();

        File outputData = outDirs.stream()
            .filter(dir -> dir.exists())
            .map(dir -> dir.toPath().resolve("output.json").toFile())
            .filter(f -> f.exists())
            .findFirst()
            .orElseThrow(() -> new ResultUnavailableException("no output.json found"));
        

        Pattern versionPtn = Pattern.compile(".*versionName\":\"(.+?)\"");
        Pattern fullNamePtn = Pattern.compile(".*fullName\":\"(.+?)\"");
        Pattern apkPathPtn = Pattern.compile(".*path\":\"(.+?)\"");
        
        String version = "undefinedVersion";
        String fullName = "undefinedFullName";
        File apk = null;

        try (Scanner in = new Scanner(new FileReader(outputData))) {
           while(in.hasNext()) {
               String line = in.nextLine();
               Matcher verMatcher = versionPtn.matcher(line);
               if (verMatcher.find()) {
                   version = verMatcher.group(1);
               }
               Matcher nameMatcher = fullNamePtn.matcher(line);
               if (nameMatcher.find()) {
                   fullName = nameMatcher.group(1);
               }
               Matcher apkMatcher = apkPathPtn.matcher(line);
               if (apkMatcher.find()) {
                   apk = outputData.toPath().getParent().resolve(apkMatcher.group(1)).toFile();
               }
           }
        } catch (FileNotFoundException | IllegalStateException | IndexOutOfBoundsException e) {
            throw new ResultUnavailableException(e);
        }
       
        if (apk == null || !apk.exists()) {
            throw new ResultUnavailableException("apk not found");
        }
        task.getLogger().warn("Found {}, content follows:", apk.getAbsolutePath());
        try (JarFile jar = new JarFile(apk)){ 
            Set<File> packedFiles = jar.stream()
                .map(je -> je.getName())
                .map(filename -> {
                    task.getLogger().warn("\t{}", filename);

                    // skip first path element e.g. assets as this is not present on filesystem
                    Path filePath = Paths.get(filename);
                    boolean skipFirstPathElem = filePath.getNameCount() > 1;

                    return inputDirs.stream()
                        .filter(dir -> dir.exists())
                        .flatMap(dir -> wrapFind(
                            dir.toPath(), 
                            (path,attrs) -> attrs.isRegularFile() && path.endsWith(skipFirstPathElem ? filePath.subpath(1, filePath.getNameCount()).toString() : filename)))
                        .map(p -> p.toFile())
                        .findFirst();
                })
                .filter(o -> o.isPresent())
                .map(o -> o.get())
                .collect(Collectors.toSet());
           
            Optional<PackageNode> pkgNode = PackagenodeUtils.processArtifacts(
                packedFiles, 
                String.format("%s-%s", task.getProject().getName(), fullName),
                version);
           
            if (pkgNode.isPresent()) {
                bsc.SendPackageNode(pkgNode.get());
            } else {
                bsc.SendLogMessage(String.format("No packagenode after processing %s", apk.getAbsolutePath()));
            }

        } catch (IOException ioe) {
                bsc.SendLogMessage(String.format("Qmstr gradle plugin failed: %s", ioe.getMessage()));
        }
    }


}